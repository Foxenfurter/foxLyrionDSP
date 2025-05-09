package LyrionDSPProcessAudio

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"runtime/debug"

	"github.com/Foxenfurter/foxAudioLib/foxAudioDecoder"
	"github.com/Foxenfurter/foxAudioLib/foxAudioEncoder"
	"github.com/Foxenfurter/foxAudioLib/foxConvolver"
	"github.com/Foxenfurter/foxAudioLib/foxLog"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPFilters"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPSettings"
)

const packageName = "LyrionDSPProcessAudio"

type AudioProcessor struct {
	Decoder       *foxAudioDecoder.AudioDecoder
	Encoder       *foxAudioEncoder.AudioEncoder
	Logger        *foxLog.Logger
	Config        *LyrionDSPSettings.ClientConfig
	Convolvers    []foxConvolver.Convolver
	ConvolverTail [][]float64
	UseTail       bool
	TailPath      string
	DelayPath     string
	AppSettings   *LyrionDSPSettings.AppSettings
	Args          *LyrionDSPSettings.Arguments
	Impulse       [][]float64
	SaveImpulse   bool
	Delay         *LyrionDSPFilters.Delay
}

// Initialize Audio Headers and create the convolvers and populate any tails data from previous runs
func (ap *AudioProcessor) Initialize() error {
	const functionName = "Initialize"
	errorText := fmt.Sprintf("%s:%s ", packageName, functionName)
	// initialise decoder
	ap.Decoder = &foxAudioDecoder.AudioDecoder{
		DebugFunc: ap.Logger.Debug,
		Type:      "WAV",
	}
	// If no input file is specified, use stdin by default
	if ap.Args.InPath != "" {
		ap.Decoder.Filename = ap.Args.InPath
	}
	ap.Logger.Debug(errorText + "Input Format: " + ap.Args.InputFormat)
	if strings.ToUpper(ap.Args.InputFormat) == "PCM" {
		ap.Decoder.Type = "PCM"
		ap.Decoder.SampleRate = ap.Args.InputSampleRate
		ap.Decoder.BitDepth = ap.Args.PCMBits
		ap.Decoder.NumChannels = ap.Args.PCMChannels
		ap.Decoder.BigEndian = ap.Args.BigEndian
	} else {
		ap.Decoder.Type = "WAV"
	}

	err := ap.Decoder.Initialise()
	if err != nil {
		// initialise an empty encode purely for error handling
		ap.Logger.Error(errorText + "Error Initialising Audio Decoder: " + err.Error())
		ap.Encoder = &foxAudioEncoder.AudioEncoder{}
		return err
	}
	// initialise encoder

	//myLogger.Debug("Initialising Encoder...")
	ap.Encoder = &foxAudioEncoder.AudioEncoder{
		Type:        "WAV",
		SampleRate:  ap.Decoder.SampleRate,
		BitDepth:    ap.Args.OutBits, // Use targetBitDepth, not myDecoder.BitDepth
		NumChannels: ap.Decoder.NumChannels,
		DebugFunc:   ap.Logger.Debug,
		DebugOn:     true,
		// use a file name here if there are issues with stdout
		//Filename:    "c:\\temp\\jonfile.wav",
	}
	if strings.ToUpper(ap.Args.OutputFormat) == "PCM" {
		ap.Encoder.Type = "PCM"
	}
	if ap.Decoder.Size != 0 {
		ap.Encoder.Size = int64(ap.Decoder.Size) * int64(ap.Args.OutBits) / int64(ap.Decoder.BitDepth) //outputSize = (inputSize * outputBitDepth) / inputBitDepth
	} else {
		ap.Encoder.Size = 0
	}

	if ap.Args.OutPath != "" {
		ap.Encoder.Filename = ap.Args.OutPath
	}
	err = ap.Encoder.Initialise()
	if err != nil {
		ap.Logger.Error(errorText + "Error Initialising Audio Encoder: " + err.Error())
		return err
	}
	// Reader used to load tail end from previously played track

	myTailReader := new(foxAudioDecoder.AudioDecoder)

	// Setup delay
	ap.Delay = LyrionDSPFilters.NewDelay(ap.Decoder.NumChannels, float64(ap.Decoder.SampleRate))
	var myBuffer [][]float64
	myDelayChannel := 0
	delayMS := 0.0
	switch delayMS = ap.Config.Delay.Value; {

	case delayMS < 0:
		myDelayChannel = 0
		ap.Logger.Debug(errorText + fmt.Sprintf(": Delay left channel %f ms", -delayMS))
		delayMS = -delayMS
		ap.DelayPath = filepath.Join(ap.AppSettings.TempDataFolder, "delay_"+ap.Args.CleanClientID+"_left.wav")

	case delayMS > 0:
		myDelayChannel = 1
		ap.Logger.Debug(errorText + fmt.Sprintf(": Delay right channel %f ms", delayMS))
		ap.DelayPath = filepath.Join(ap.AppSettings.TempDataFolder, "delay_"+ap.Args.CleanClientID+"_right.wav")

	}
	// look for delay file if there is a delay
	if delayMS != 0 {

		// add delay
		ap.Delay.AddDelay(myDelayChannel, delayMS)
		// load delay tail
		myBuffer, err = myTailReader.LoadFiletoSampleBuffer(ap.DelayPath, "WAV", ap.Logger)
		if err != nil {
			ap.Logger.Debug(errorText + fmt.Sprintf("Error loading delay tail: %v", err))

		} else {
			if myTailReader.SampleRate != ap.Decoder.SampleRate {
				ap.Logger.Debug(errorText + fmt.Sprintf("Delay tail not used as sample rate %d does not match decoder sample rate %d",
					myTailReader.SampleRate, ap.Decoder.SampleRate))
			} else {
				// no error and sample rates match so add delay tail - handle scenarios where the tail length is different to the target delay
				// for example if the delay was changed whilst track was playing.
				ap.Logger.Debug(errorText + fmt.Sprintf(": Delay tail loaded successfully %d samples", len(myBuffer[0])))
				targetBuffer := ap.Delay.Buffers[myDelayChannel] // Reference to the existing buffer
				sourceBuffer := myBuffer[0]                      // Source delay tail data

				// Determine how many samples to copy (min of source/target length)
				samplesToCopy := len(sourceBuffer)
				if samplesToCopy > len(targetBuffer) {
					samplesToCopy = len(targetBuffer)
				}

				// Copy samples without resizing the target buffer
				copy(targetBuffer[:samplesToCopy], sourceBuffer[:samplesToCopy])

				// Zero remaining samples if source is shorter than target
				if len(sourceBuffer) < len(targetBuffer) {
					for i := samplesToCopy; i < len(targetBuffer); i++ {
						targetBuffer[i] = 0.0
					}
				}

				//ap.Delay.Buffers[myDelayChannel] = myBuffer[0]
			}
		}
		myTailReader.Close()
		// Delete the tail file - we want it gone!
		foxAudioEncoder.DeleteFile(ap.DelayPath, ap.Logger)
	}
	// Initialise Convolvers
	targetSampleRate := ap.Decoder.SampleRate
	//used for normalization - may need to add this as a configurable item in the future
	targetLevel := 0.75
	saveImpulse := false
	myTempFirFilter := ""

	baseFileName := strings.TrimSuffix(filepath.Base(ap.Config.FIRWavFile), filepath.Ext(ap.Config.FIRWavFile))
	// no impulse
	//myLogger.Debug("Trying to load impulse: " + baseFileName)
	if baseFileName == "." {
		ap.Logger.Debug(errorText + ": No impulse specified")
		ap.Impulse = make([][]float64, 1)
	} else {

		baseFileName = baseFileName + "_" + fmt.Sprintf("%d", targetSampleRate) + ".wav"
		myTempFirFilter = filepath.Join(ap.AppSettings.TempDataFolder, baseFileName)

		//inputFile string, outputFile string, targetSampleRate int, targetBitDepth int, myLogger *foxLog.Logger
		// try and load resampled file first
		ap.Logger.Debug(errorText + "Trying resampled Impulse First : " + myTempFirFilter)
		ap.Impulse, err = LyrionDSPFilters.LoadImpulse(myTempFirFilter, targetSampleRate, targetLevel, ap.Logger)
		if err != nil {
			// if that fails then try with original file
			if strings.Contains(err.Error(), "does not exist") {
				// File not found case
				ap.Logger.Debug(errorText + "Resampled Impulse does not exist, trying original: " + ap.Config.FIRWavFile)
				ap.Impulse, err = LyrionDSPFilters.LoadImpulse(ap.Config.FIRWavFile, targetSampleRate, targetLevel, ap.Logger)
				if err != nil {
					ap.Logger.Warn(errorText + "Error loading impulse: " + err.Error())
				} else {
					saveImpulse = true
				}
			} else {
				ap.Logger.Warn(errorText + "Error loading impulse: " + err.Error()) // Other errors
			}
		}
	}

	ap.Logger.Debug("Create PEQ Filter")
	myPEQ, err := LyrionDSPFilters.BuildPEQFilter(ap.Config, ap.AppSettings, targetSampleRate, ap.Logger)
	if err != nil {
		ap.Logger.FatalError(errorText + "Error building PEQ: " + err.Error())

	}
	ap.Logger.Debug("Combine Filters")
	myConvolvers, err := LyrionDSPFilters.CombineFilters(ap.Impulse, *myPEQ, ap.Decoder.NumChannels, targetSampleRate, ap.Logger)
	if err != nil {
		ap.Logger.Error(errorText + "Error combining filters: " + err.Error())
	}
	// set the convolvers
	ap.Convolvers = myConvolvers
	// save the impulse
	if saveImpulse {
		// Backup Impulses
		targetBitDepth := 32
		ap.Logger.Debug("Backup Impulse: " + myTempFirFilter)
		go foxAudioEncoder.WriteWavFile(
			myTempFirFilter,
			ap.Impulse,
			targetSampleRate,
			targetBitDepth,
			len(ap.Impulse),
			false, // i.e. do not overwrite
			ap.Logger,
		)
	} else {
		ap.Logger.Debug(errorText + "No impulse to backup")
	}
	// Load any Convolver Tail from previous track
	ap.UseTail = true
	baseFileName = "convolver_" + ap.Args.CleanClientID + "_tail.wav"
	ap.TailPath = filepath.Join(ap.AppSettings.TempDataFolder, baseFileName)

	ap.ConvolverTail, err = myTailReader.LoadFiletoSampleBuffer(ap.TailPath, "WAV", ap.Logger)
	if err != nil {
		ap.Logger.Debug(errorText + fmt.Sprintf("Error loading convolver tail: %v", err))
		ap.UseTail = false
	} else {
		if myTailReader.SampleRate != ap.Decoder.SampleRate || myTailReader.NumChannels != ap.Decoder.NumChannels {
			ap.Logger.Debug(errorText + fmt.Sprintf("Convolver tail not used as sample rate %d does not match decoder sample rate %d",
				myTailReader.SampleRate, ap.Decoder.SampleRate))
			ap.UseTail = false
		} else {
			ap.Logger.Debug(errorText + fmt.Sprintf("Convolver tail loaded: %s, %d samples", ap.TailPath, len(ap.ConvolverTail[0])))
			// now assign convolver tail to convolvers
			for i := range ap.Convolvers {
				ap.Convolvers[i].SetTail(ap.ConvolverTail[i])
			}
		}
	}
	if !ap.UseTail {
		ap.ConvolverTail = make([][]float64, ap.Decoder.NumChannels)
	}
	myTailReader.Close()
	// Delete the tail file - we want it gone!
	foxAudioEncoder.DeleteFile(ap.TailPath, ap.Logger)

	return nil
}

// ProcessAudio handles the main audio processing workflow
// uses channels to pass data between the different processing stages, which are defined as go routines
func (ap *AudioProcessor) ProcessAudio() {
	const functionName = "ProcessAudio"
	errorText := fmt.Sprintf("%s:%s: ", packageName, functionName)

	var wg sync.WaitGroup
	exitCode := 0
	isPipe := false

	if stat, _ := os.Stdout.Stat(); (stat.Mode() & os.ModeCharDevice) == 0 {
		isPipe = true
		ap.Logger.Debug(errorText + "Data sourced from stdin")
	}

	myOS := runtime.GOOS
	// bumping these up does not seem to make much difference
	decodedBuffer, channelBuffer, mergedBuffer, feedbackBuffer := 24, 16, 16, 1

	if myOS == "windows" {
		decodedBuffer, channelBuffer, mergedBuffer, feedbackBuffer = 4, 2, 2, 1
	}

	var (
		DecodedSamplesChannel chan [][]float64
		audioChannels         = make([]chan []float64, ap.Decoder.NumChannels)
		convolvedChannels     = make([]chan []float64, ap.Decoder.NumChannels)
		scaledChannels        = make([]chan []byte, ap.Decoder.NumChannels)
		finishedChannel       chan bool
		mergedChannel         chan [][]float64
		feedbackChannel       chan int64
	)

	DecodedSamplesChannel = make(chan [][]float64, decodedBuffer)
	mergedChannel = make(chan [][]float64, mergedBuffer)
	feedbackChannel = make(chan int64, feedbackBuffer)
	finishedChannel = make(chan bool, feedbackBuffer)

	for i := range audioChannels {
		audioChannels[i] = make(chan []float64, channelBuffer)
		convolvedChannels[i] = make(chan []float64, channelBuffer)
		scaledChannels[i] = make(chan []byte, channelBuffer)
	}

	//We do not use feedback on windows either!
	//if myOS != "windows" {
	feedbackChannel = nil
	//}
	ap.Logger.Debug(errorText + "Setup Audio Channels")

	wg.Add(1)
	go ap.channelSplitter(DecodedSamplesChannel, audioChannels, &wg)

	// Convolution setup
	ap.Logger.Debug(errorText + "Setting up Channel Convolver...")
	for i := range ap.Decoder.NumChannels {
		//myConvolver := foxConvolver.NewConvolver(ap.Convolvers[i].FilterImpulse)
		if ap.UseTail {
			ap.Logger.Debug(errorText + fmt.Sprintf("Convolver tail length: %d and Overlap length %d Channel %d",
				len(ap.ConvolverTail[i]), len(ap.Convolvers[i].FilterImpulse), i))
		} else {
			ap.Logger.Debug(errorText + fmt.Sprintf("No Convolver tail loaded Channel %d", i))
		}
		ap.Convolvers[i].DebugOn = true
		ap.Convolvers[i].DebugFunc = ap.Logger.Debug
		wg.Add(1)

		go func(ch int) {
			defer func() {
				close(convolvedChannels[ch])
				ap.Logger.Debug(errorText + fmt.Sprintf("Convolution channel %d closed", ch))
				ap.ConvolverTail[ch] = ap.Convolvers[ch].GetTail()
				wg.Done()
				ap.Logger.Debug(errorText + fmt.Sprintf("Convolution for channel %d done", ch))
			}()
			ap.Convolvers[ch].ConvolveChannel(audioChannels[ch], convolvedChannels[ch])
		}(i)
	}

	// Channel merging
	ap.Logger.Debug(errorText + "Setting up Channel Merger...")
	wg.Add(1)
	go func() {
		defer func() {
			close(mergedChannel)
			ap.Logger.Debug(errorText + "Merge channel closed")
			wg.Done()
			//switch on Garbage Collection
			debug.SetGCPercent(100)
			ap.Logger.Debug(errorText + "Merge channel done")
		}()
		//switch off Garbage Collection
		debug.SetGCPercent(-1)
		// clear garbage and free memory
		runtime.GC()
		debug.FreeOSMemory()
		ap.mergeChannels(convolvedChannels, mergedChannel)
	}()

	// Encoding
	ap.Logger.Debug(errorText + "Setting up Encoder... ")
	wg.Add(1)
	go func() {
		defer func() {

			if finishedChannel != nil {
				finishedChannel <- true
			}
			wg.Done()
			ap.Logger.Debug(errorText + fmt.Sprintf("Encoding Done... %d", exitCode))
		}()
		err := ap.Encoder.EncodeSamplesChannel(mergedChannel, feedbackChannel)
		if err != nil {
			if isPipe {
				switch {
				case errors.Is(err, syscall.EPIPE):
					ap.Logger.Error(errorText + "encoder: broken pipe (SIGPIPE) - output closed")
					ap.Terminate()
					//exitCode = 2
					//return
				case errors.Is(err, io.ErrClosedPipe):
					ap.Logger.Error(errorText + "encoder: output pipe closed prematurely")
					ap.Terminate()
					//exitCode = 3
					//return
				}
			}
			ap.Logger.Error(errorText + fmt.Errorf("encoder error: %w", err).Error())
			ap.Terminate()
			exitCode = 1
		}
		ap.Logger.Debug(errorText + "Finished Encoding... ")
	}()

	// Decoding
	wg.Add(1)
	ap.Logger.Debug(errorText + "Decoding Data...")
	go func() {
		defer func() {
			close(DecodedSamplesChannel)
			ap.Logger.Debug(errorText + "Finished Decoding Data...")
			for {
				if feedbackChannel != nil {
					ws := <-feedbackChannel
					if ws == 0 {
						break
					}
					time.Sleep(200 * time.Millisecond)
				} else {
					break
				}
			}
			writerFinished := false
			for {
				if finishedChannel != nil {
					writerFinished = <-finishedChannel
					if writerFinished {
						break
					}
					time.Sleep(200 * time.Millisecond)
				}
			}
			wg.Done()
			ap.Logger.Debug(errorText + "Decoding and writer Done...")
		}()
		ap.Decoder.DecodeSamples(DecodedSamplesChannel, feedbackChannel)
	}()

	ap.Logger.Debug(errorText + "Waiting for processing to complete...")
	wg.Wait()

	if len(ap.ConvolverTail[0]) > 0 {
		ap.Logger.Debug(errorText + "Backing up convolver tail")
		foxAudioEncoder.WriteWavFile(
			ap.TailPath,
			ap.ConvolverTail,
			ap.Decoder.SampleRate,
			32,
			len(ap.ConvolverTail),
			true,
			ap.Logger,
		)
	} else {
		ap.Logger.Debug(errorText + "No tail to backup")
	}

	ap.Logger.Debug(errorText + " Processing Complete...")
	if err := ap.Encoder.Close(); err != nil {
		ap.Logger.Warn(errorText + " Error closing output file: " + err.Error())
	}
}

// Splits channels for parallel convolution and adds delays if they have been specificed
func (ap *AudioProcessor) channelSplitter(
	inputCh chan [][]float64,
	outputChs []chan []float64,
	WG *sync.WaitGroup,

) {
	errorText := packageName + "Channel Splitter: "
	chunkCounter := 0
	channelCount := ap.Decoder.NumChannels
	go func() {

		defer func() {
			// Flush remaining data in buffers
			// don't want to do this as any remaining delay should be saved to file and loaded next time.
			for i := 0; i < channelCount; i++ {

				close(outputChs[i])
				ap.Logger.Debug(errorText + fmt.Sprintf("splitter closed channel %d", i))
				if len(ap.Delay.Buffers[i]) > 0 {
					ap.Logger.Debug(errorText + "Backing up delay tail")
					foxAudioEncoder.WriteWavFile(
						ap.DelayPath,
						[][]float64{ap.Delay.Buffers[i]},
						ap.Decoder.SampleRate,
						32,
						1, // number of channels
						true,
						ap.Logger,
					)
				} else {
					ap.Logger.Debug(errorText + "No tail to backup")
				}

			}
			WG.Done()
			ap.Logger.Debug(packageName + ":Channel Splitter Done... " +
				fmt.Sprintf("%d chunks processed", chunkCounter))
		}()
		retainStart := 0
		for chunk := range inputCh {
			for i := range channelCount {

				if ap.Delay.Delays[i] > 0 {
					//channelData := chunk[i]
					// Prepend buffer to current data combined is now longer than the chunk
					combined := append(ap.Delay.Buffers[i], chunk[i]...)
					// We should always output the full chunk and no more
					//outputLength := len(channelData)
					//output delay plus initial part of the chunk to give us a chunks worth of data
					outputChs[i] <- combined[:len(chunk[i])]

					// Retain last `delay.delays[i]` samples for next iteration
					retainStart = max(len(combined)-ap.Delay.Delays[i], 0)
					ap.Delay.Buffers[i] = combined[retainStart:]
				} else {
					outputChs[i] <- chunk[i] // No delay
				}
			}
			chunkCounter++
		}
	}()
}

func (ap *AudioProcessor) mergeChannels(inputChannels []chan []float64, outputChannel chan [][]float64) {
	const functionName = "mergeChannels"
	errorText := fmt.Sprintf("%s:%s", packageName, functionName)
	defer ap.Logger.Debug(errorText + ": All channels drained")
	numChannels := ap.Decoder.NumChannels

	channelGains := LyrionDSPFilters.GetChannelsGain(ap.Config, numChannels, ap.Logger)
	if numChannels > 1 {
		ap.Logger.Debug(errorText + fmt.Sprintf(" : Setup, channel gain left %f, right %f", channelGains[0], channelGains[1]))
	}

	var sigmaGain, deltaGain float64
	var applyWidth bool
	if ap.Config.Width != 0.0 && numChannels > 1 {
		applyWidth = true
		sigmaGain, deltaGain = LyrionDSPFilters.GetWidthCoefficients(ap.Config.Width)
		ap.Logger.Debug(errorText + fmt.Sprintf(" : Setup, width delta %f, sigma %f", deltaGain, sigmaGain))
	}

	activeChannels := numChannels
	var mergedChunks [][]float64
	var mid, side float64
	allReady := true

	for activeChannels > 0 {
		// Reset mergedChunks for new data
		mergedChunks = make([][]float64, numChannels) // Read from all channels, blocking until data is available
		for i := range numChannels {
			if inputChannels[i] == nil {
				continue // Channel closed
			}

			chunk, ok := <-inputChannels[i]
			if !ok {
				inputChannels[i] = nil
				activeChannels--
				ap.Logger.Debug(errorText + fmt.Sprintf(" : Input Channel %d closed", i))
				continue
			}

			// Apply gain
			for j := range chunk {
				chunk[j] *= channelGains[i]
			}
			mergedChunks[i] = chunk
		}

		// Check if all active channels have data
		allReady = true
		for i := range numChannels {
			if inputChannels[i] != nil && mergedChunks[i] == nil {
				allReady = false
				break
			}
		}

		if allReady {
			if applyWidth {
				for mc := range mergedChunks[0] {
					mid = (mergedChunks[0][mc] + mergedChunks[1][mc]) * sigmaGain
					side = (mergedChunks[0][mc] - mergedChunks[1][mc]) * deltaGain
					mergedChunks[0][mc] = mid + side
					mergedChunks[1][mc] = mid - side
				}
			}
			// Send the processed chunks
			outputChannel <- mergedChunks
			// Manually run GC and free OS memory
			runtime.GC()
			debug.FreeOSMemory()

		}
	}
}

func (ap *AudioProcessor) Terminate() {
	const functionName = "Terminate"

	peakDBFS := PeakDBFS(ap.Encoder.Peak)
	ErrorText := fmt.Sprintf("%s:%s: ", packageName, functionName)
	ap.Logger.Info(ErrorText + "Closing because of output termination...")
	//11423050 samples, 241703.3034 ms (659.6813 init), 1.0717 * realtime, peak -8.6029 dBfs
	expectedSeconds := float64(ap.Encoder.NumSamples) / float64(ap.Encoder.SampleRate)
	relativeSpeed := expectedSeconds / 1.0

	rawPeakDBFS := PeakDBFS(ap.Decoder.RawPeak)
	ap.Logger.Debug(ErrorText + fmt.Sprintf("rawPeak %f Input Peak %f OutputPeak %f Diff %f", ap.Decoder.RawPeak, rawPeakDBFS, peakDBFS, peakDBFS-rawPeakDBFS))

	// Go code to match C# log format
	ap.Logger.Info(fmt.Sprintf(
		"%d samples, %.3f ms (%.3f init), %.4f * realtime, peak %.4f dBfs, input peak %.4f dBfs \n",
		ap.Encoder.NumSamples, // n (samples)
		0.0,                   // Convert seconds to milliseconds (e.g., 103.810 ms)
		0.0,                   // Convert init time to milliseconds
		relativeSpeed,         // realtime/runtime (e.g., 1.255)
		peakDBFS,              // dBfs peak value
		rawPeakDBFS,           // Input peak value
	))
	// Close the output file after all processing is done
	os.Exit(1)

}

func PeakDBFS(peak float64) float64 {
	if peak == 0 {
		return math.Inf(-1)
	}
	return 20 * math.Log10(peak)
}

// ByPassProcess handles the  processing workflow when bypass is enabled and the input and output formats are different
// uses channels to pass data between the different processing stages, which are defined as go routines
func (ap *AudioProcessor) ByPassProcess() {
	const functionName = "ByPassProcess"
	errorText := fmt.Sprintf("%s:%s: ", packageName, functionName)
	ap.Logger.Info(errorText + "Bypass mode enabled - minimal processing")
	var wg sync.WaitGroup
	exitCode := 0
	isPipe := false

	if stat, _ := os.Stdout.Stat(); (stat.Mode() & os.ModeCharDevice) == 0 {
		isPipe = true
		ap.Logger.Debug(errorText + "Data sourced from stdin")
	}

	myOS := runtime.GOOS
	decodedBuffer, feedbackBuffer := 1, 1
	if myOS == "windows" {
		decodedBuffer, feedbackBuffer = 1, 1
	}

	var (
		DecodedSamplesChannel chan [][]float64

		finishedChannel chan bool

		feedbackChannel chan int64
	)

	DecodedSamplesChannel = make(chan [][]float64, decodedBuffer)

	feedbackChannel = make(chan int64, feedbackBuffer)
	finishedChannel = make(chan bool, feedbackBuffer)

	if myOS != "windows" {
		feedbackChannel = nil
	}

	// Encoding
	ap.Logger.Debug(errorText + "Setting up Encoder... ")
	wg.Add(1)
	go func() {
		defer func() {

			if finishedChannel != nil {
				finishedChannel <- true
			}
			wg.Done()
			ap.Logger.Debug(errorText + fmt.Sprintf("Encoding Done... %d", exitCode))
		}()
		err := ap.Encoder.EncodeSamplesChannel(DecodedSamplesChannel, feedbackChannel)
		if err != nil {
			if isPipe {
				switch {
				case errors.Is(err, syscall.EPIPE):
					ap.Logger.Error(errorText + "encoder: broken pipe (SIGPIPE) - output closed")
					ap.Terminate()
					//exitCode = 2
					//return
				case errors.Is(err, io.ErrClosedPipe):
					ap.Logger.Error(errorText + "encoder: output pipe closed prematurely")
					ap.Terminate()
					//exitCode = 3
					//return
				}
			}
			ap.Logger.Error(errorText + fmt.Errorf("encoder error: %w", err).Error())
			ap.Terminate()
			exitCode = 1
		}
		ap.Logger.Debug(errorText + "Finished Encoding... ")
	}()

	// Decoding
	wg.Add(1)
	ap.Logger.Debug(errorText + "Decoding Data...")
	go func() {
		defer func() {
			close(DecodedSamplesChannel)
			ap.Logger.Debug(errorText + "Finished Decoding Data...")
			for {
				if feedbackChannel != nil {
					ws := <-feedbackChannel
					if ws == 0 {
						break
					}
					time.Sleep(200 * time.Millisecond)
				} else {
					break
				}
			}
			writerFinished := false
			for {
				if finishedChannel != nil {
					writerFinished = <-finishedChannel
					if writerFinished {
						break
					}
					time.Sleep(200 * time.Millisecond)
				}
			}
			wg.Done()
			ap.Logger.Info(errorText + "Decoding and writer Done...")
		}()
		ap.Decoder.DecodeSamples(DecodedSamplesChannel, feedbackChannel)
	}()

	ap.Logger.Debug(errorText + "Waiting for processing to complete...")
	wg.Wait()

	ap.Logger.Debug(errorText + " Processing Complete...")
	if err := ap.Encoder.Close(); err != nil {
		ap.Logger.Warn(errorText + " Error closing output file: " + err.Error())
	}
}
