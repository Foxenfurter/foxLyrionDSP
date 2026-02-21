package LyrionDSPProcessAudio

import (
	"encoding/gob"
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

	"github.com/Foxenfurter/foxAudioLib/foxAudioDecoder"
	"github.com/Foxenfurter/foxAudioLib/foxAudioEncoder"
	foxConvolver "github.com/Foxenfurter/foxAudioLib/foxConvolverPartition"
	"github.com/Foxenfurter/foxAudioLib/foxLog"
	"github.com/Foxenfurter/foxAudioLib/foxResampler"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPFilters"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPSettings"
)

const packageName = "LyrionDSPProcessAudio"

type AudioProcessor struct {
	Decoder    *foxAudioDecoder.AudioDecoder
	Encoder    *foxAudioEncoder.AudioEncoder
	Logger     *foxLog.Logger
	Config     *LyrionDSPSettings.ClientConfig
	Convolvers []*foxConvolver.PartitionedConvolver
	//ConvolverTail   [][]float64
	//ConvolverBuffer [][]float64
	//UseTail        bool
	TempFilterPath string
	cachePath      string

	//BufferPath  string
	//TailPath    string
	//DelayPath   string
	AppSettings *LyrionDSPSettings.AppSettings
	Args        *LyrionDSPSettings.Arguments
	Impulse     [][]float64
	SaveImpulse bool
	PEQImpulse  []float64
	Delay       *LyrionDSPFilters.Delay
	ReuseFilter bool
}

const CACHE_VERSION = 2 // Increment when changing convolver type
type DSPResidualsCache struct {
	Version     int
	SampleRate  int
	NumChannels int
	ExpiryTime  int64 // When this cache expires
	//ConvolverTails   [][]float64
	//ConvolverBuffers [][]float64
	DelayBuffers    *LyrionDSPFilters.Delay // Full delay buffers for all channels
	ConvolverStates [][]byte                // Full encoded state
}

// Initialize Audio Headers and create the convolvers and populate any tails data from previous runs
// The signal block length is calculated in DSP Filters combine filters function

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

	targetSampleRate := ap.Decoder.SampleRate

	// Reader used to load pre-computer filter, delay tails and filter tails from previously played track
	ap.ReuseFilter = false
	ap.TempFilterPath = filepath.Join(ap.AppSettings.TempDataFolder, "filter_"+ap.Args.CleanClientID+"_"+fmt.Sprintf("%d", targetSampleRate)+".gob")

	// initialise delay
	myTempReader := new(foxAudioDecoder.AudioDecoder)
	ap.cachePath = filepath.Join(ap.AppSettings.TempDataFolder, "dsp_residuals_"+ap.Args.CleanClientID+".gob")
	ap.Delay = LyrionDSPFilters.NewDelay(ap.Decoder.NumChannels, float64(targetSampleRate))
	//var myBuffer [][]float64
	myDelayChannel := 0
	delayMS := 0.0

	switch delayMS = ap.Config.Delay.Value; {

	case delayMS < 0:
		myDelayChannel = 0
		ap.Logger.Debug(errorText + fmt.Sprintf(": Delay left channel %f ms", -delayMS))
		delayMS = -delayMS
		//ap.DelayPath = filepath.Join(ap.AppSettings.TempDataFolder, "delay_"+ap.Args.CleanClientID+"_left.wav")

	case delayMS > 0:
		myDelayChannel = 1
		ap.Logger.Debug(errorText + fmt.Sprintf(": Delay right channel %f ms", delayMS))
		//ap.DelayPath = filepath.Join(ap.AppSettings.TempDataFolder, "delay_"+ap.Args.CleanClientID+"_right.wav")

	}
	// look for delay file if there is a delay
	if delayMS != 0 {
		// add delay
		ap.Delay.AddDelay(myDelayChannel, delayMS)

	}

	// Initialise Convolvers

	//used for normalization - may need to add this as a configurable item in the future
	// Approx -0.5 dBFS
	//targetLevel := 1.0
	saveImpulse := false
	myTempFirFilter := ""
	baseFileName := ""

	// Load any Convolver from previous track
	ap.Logger.Debug(errorText + ": " + "Attemptng to load cached convolvers from " + ap.TempFilterPath)
	err = ap.LoadConvolvers(ap.TempFilterPath, ap.Config.TimeStamp)
	if err != nil {
		ap.Logger.Debug(errorText + ": " + err.Error())
		ap.ReuseFilter = false
	} else {
		ap.ReuseFilter = true
		ap.Logger.Debug(errorText + ": " + "Convolver loaded from cache")
		//ap.UseTail = true
		ap.LoadDSPResiduals()
	}
	if !ap.ReuseFilter {

		var wg sync.WaitGroup

		wg.Add(2)
		go func() {
			defer wg.Done()

			baseFileName = strings.TrimSuffix(filepath.Base(ap.Config.FIRWavFile), filepath.Ext(ap.Config.FIRWavFile))
			// no impulse
			//myLogger.Debug("Trying to load impulse: " + baseFileName)
			if baseFileName == "." {
				ap.Logger.Debug(errorText + ": No impulse specified")
				ap.Impulse = make([][]float64, 0)
			} else {

				//baseFileName = baseFileName + "_" + fmt.Sprintf("%d", targetSampleRate) + ".wav"
				baseFileName = baseFileName + "_" + ap.Args.CleanClientID + "_" + fmt.Sprintf("%d", targetSampleRate) + ".wav"
				myTempFirFilter = filepath.Join(ap.AppSettings.TempDataFolder, baseFileName)

				//inputFile string, outputFile string, targetSampleRate int, targetBitDepth int, myLogger *foxLog.Logger
				// try and load resampled file first
				ap.Logger.Debug(errorText + "Trying resampled Impulse First : " + myTempFirFilter)
				ap.Impulse, err = myTempReader.LoadFiletoSampleBuffer(myTempFirFilter, "WAV", ap.Logger)
				if err != nil {
					// if that fails then try with original file
					if strings.Contains(err.Error(), "does not exist") {
						// File not found case
						ap.Logger.Debug(errorText + "Resampled Impulse does not exist, trying original: " + ap.Config.FIRWavFile)
						//ap.Impulse, err = LyrionDSPFilters.LoadImpulse(ap.Config.FIRWavFile, targetSampleRate, targetLevel, ap.Logger)

						myResampler := foxResampler.NewResampler()
						myResampler.ToSampleRate = targetSampleRate
						myResampler.SOXPath = ap.AppSettings.SoxExe

						ap.Impulse, err = myResampler.ReadnResampleFile2Buffer(ap.Config.FIRWavFile)
						if err != nil {
							ap.Logger.Warn(errorText + "Error loading impulse: " + err.Error())
						}
						// remove silence and background noise from impulse

						// normalize impulse
						//ap.Impulse, err = LyrionDSPFilters.NormalizeImpulse(ap.Impulse, targetLevel, ap.Logger)
						//if err != nil {
						//	ap.Logger.Warn(errorText + "Error normalising impulse: " + err.Error())
						//}
						ap.Impulse, err = LyrionDSPFilters.CleanUpImpulse(ap.Impulse, targetSampleRate, -70.0, ap.Logger)
						if err != nil {
							ap.Logger.Warn(errorText + "Error cleaning impulse: " + err.Error())
						}
						saveImpulse = true

					} else {
						ap.Logger.Warn(errorText + "Error loading impulse: " + err.Error()) // Other errors
					}
				}
			}
		}()

		myPEQ := LyrionDSPFilters.NewPEQFilter(targetSampleRate, ap.Logger)
		go func() {
			defer wg.Done()
			ap.Logger.Debug("Create PEQ Filter")
			var err error = nil
			myPEQ, err = LyrionDSPFilters.BuildPEQFilter(ap.Config, ap.AppSettings, targetSampleRate, ap.Logger)
			if err != nil {
				ap.Logger.FatalError(errorText + "Error building PEQ: " + err.Error())
			}
			//ap.PEQImpulse = myPEQ.Impulse

		}()
		wg.Wait()

		// test an amended combine filters which handles all the complex logic around the different cases of FIR and PEQ filters being present or not, and also handles normalization and resampling of the filters as needed
		ap.Logger.Debug("Combine Filters")
		myConvolvers, err := LyrionDSPFilters.CombineFilters(ap.Impulse, myPEQ, ap.Decoder.NumChannels, targetSampleRate, ap.Logger)
		if err != nil {
			ap.Logger.Error(errorText + "Error combining filters: " + err.Error())
		}
		// set the convolvers
		ap.Convolvers = myConvolvers

		// lets test the combined impulse
		//	var myImpulse [][]float64
		saveImpulse = true

		// save the impulse
		if saveImpulse && len(ap.Impulse) > 0 {
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
		//ap.UseTail = false
		// Only Cache the Convolver state if they have been rebuilt
		ap.Logger.Debug("Cache Convolvers : " + ap.TempFilterPath)
		go ap.SaveConvolvers(ap.TempFilterPath)

	}

	// initialise encoder

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
		// Calculate maximum safe data size (accounts for 36-byte header overhead)
		maxSafeSize := int64(math.MaxUint32) - 36

		if ap.Encoder.Size > maxSafeSize {
			// For sizes that would overflow header, treat as "unknown"
			ap.Encoder.Size = 0
		}
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
	// Finished
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
	// bumping these up does not seem to make much difference1
	decodedBuffer, splitterBuffer, channelBuffer, mergedBuffer, feedbackBuffer := 4, 2, 1, 1, 1
	// for windows we need to be mindful of LMS socketwrapper, which kills processes 2 seconds after load completes
	// we therefore need to keep the buffer sizes small, on Linux no such issues
	if myOS == "windows" {
		decodedBuffer, splitterBuffer, channelBuffer, mergedBuffer, feedbackBuffer = 4, 2, 1, 1, 1
	}

	var (
		DecodedSamplesChannel chan [][]float64
		audioChannels         = make([]chan []float64, ap.Decoder.NumChannels)
		convolvedChannels     = make([]chan []float64, ap.Decoder.NumChannels)
		scaledChannels        = make([]chan []byte, ap.Decoder.NumChannels)
		finishedChannel       chan bool
		mergedChannel         chan [][]float64
	)

	finishedChannel = make(chan bool, feedbackBuffer)

	DecodedSamplesChannel = make(chan [][]float64, decodedBuffer)
	mergedChannel = make(chan [][]float64, mergedBuffer)

	for i := range audioChannels {
		audioChannels[i] = make(chan []float64, splitterBuffer)
		convolvedChannels[i] = make(chan []float64, channelBuffer)
		scaledChannels[i] = make(chan []byte, channelBuffer)
	}

	//We do not use feedback on windows either!
	//if myOS != "windows" {

	//}
	ap.Logger.Debug(errorText + "Setup Audio Channels")

	wg.Add(1)
	go ap.channelSplitter(DecodedSamplesChannel, audioChannels, &wg)

	// Convolution setup
	ap.Logger.Debug(errorText + "Setting up Channel Convolver...")

	for i := range ap.Decoder.NumChannels {
		/*
			if ap.UseTail {
				ap.Logger.Debug(errorText + fmt.Sprintf("Convolver  Impulse length %d Channel %d",
					len(ap.Convolvers[i].FilterImpulse), i))
			} else {
				ap.Logger.Debug(errorText + fmt.Sprintf("No Convolver Residuals loaded Channel %d", i))
			}*/
		ap.Convolvers[i].DebugOn = true //true
		ap.Convolvers[i].DebugFunc = ap.Logger.Debug
		wg.Add(1)

		go func(ch int) {
			defer func() {
				close(convolvedChannels[ch])
				ap.Logger.Debug(errorText + fmt.Sprintf("Convolution channel %d closed", ch))
				// copy residual data
				//ap.ConvolverTail[ch] = ap.Convolvers[ch].GetTail()
				//ap.ConvolverBuffer[ch] = ap.Convolvers[ch].Buffer

				ap.Logger.Debug(errorText + fmt.Sprintf("Convolution for channel %d done", ch))
				wg.Done()
			}()
			// convolve .2 second of audio at a time

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

			//ap.Logger.Debug(errorText + "Merge channel done")
			wg.Done()

		}()
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
			ap.Logger.Debug(errorText + fmt.Sprintf("Encoding Done... %d", exitCode))
			wg.Done()
		}()
		err := ap.Encoder.EncodeSamplesChannel(mergedChannel)
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

			writerFinished := false
			for {
				if finishedChannel != nil {
					writerFinished = <-finishedChannel
					if writerFinished {
						break
					}
					time.Sleep(100 * time.Millisecond)
				}

			}
			ap.Logger.Debug(errorText + "Decoding and writer Done...")
			wg.Done()

		}()
		ap.Decoder.DecodeSamples(DecodedSamplesChannel)
	}()

	ap.Logger.Debug(errorText + "Waiting for processing to complete...")
	wg.Wait()

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
			for i := range channelCount {

				close(outputChs[i])
				ap.Logger.Debug(errorText + fmt.Sprintf("splitter closed channel %d", i))

			}
			WG.Done()
			ap.Logger.Debug(packageName + ":Channel Splitter Done... " +
				fmt.Sprintf("%d chunks processed", chunkCounter))
		}()
		retainStart := 0
		chunksizeLogged := false
		// report length of delay buffers
		for i := range channelCount {
			ap.Logger.Debug(packageName + ":Channel Delay Buffer... " +
				fmt.Sprintf("Channel %d, Length %d", i, len(ap.Delay.Buffers[i])))
		}

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
					if !chunksizeLogged {
						ap.Logger.Debug(errorText + fmt.Sprintf("Chunk size %d", len(chunk[i])))

						chunksizeLogged = true

					}
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

// We are merging channels following convolution, and we are also applying individual channel gains and an
// overall width adjustment at this stage, as this is more efficient than doing it before convolution
func (ap *AudioProcessor) mergeChannels(inputChannels []chan []float64, outputChannel chan [][]float64) {
	const functionName = "mergeChannels"
	errorText := fmt.Sprintf("%s:%s", packageName, functionName)
	defer ap.Logger.Debug(errorText + ": All channels drained")
	numChannels := ap.Decoder.NumChannels
	convolverGain := 0.0
	channelGains := LyrionDSPFilters.GetChannelsGain(ap.Config, numChannels, ap.Logger)
	for i := range numChannels {
		convolverGain = math.Max(ap.Convolvers[i].MaxGain, convolverGain)
	}
	if convolverGain > 0 {

		convolverGain = 1.0 / convolverGain
		ap.Logger.Debug(errorText + fmt.Sprintf(" : Setup, convolver gain %f", convolverGain))
		for i := range numChannels {
			channelGains[i] = convolverGain * channelGains[i]
		}
	}

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
	chunksizeLogged := false

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
			if !chunksizeLogged {
				ap.Logger.Debug(errorText + fmt.Sprintf("Chunk size %d", len(chunk)))

				chunksizeLogged = true

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
					mid = (mergedChunks[0][mc] + mergedChunks[1][mc]) * sigmaGain  // sigmaGain is the 'mid' coefficient
					side = (mergedChunks[0][mc] - mergedChunks[1][mc]) * deltaGain // deltaGain is the 'side' coefficient
					mergedChunks[0][mc] = mid + side                               // Left output (reconstructed)
					mergedChunks[1][mc] = mid - side                               // Right output (reconstructed)
				}
			}
			// Send the processed chunks
			outputChannel <- mergedChunks

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
		err := ap.Encoder.EncodeSamplesChannel(DecodedSamplesChannel)
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
		ap.Decoder.DecodeSamples(DecodedSamplesChannel)
	}()

	ap.Logger.Debug(errorText + "Waiting for processing to complete...")
	wg.Wait()

	ap.Logger.Debug(errorText + " Processing Complete...")
	if err := ap.Encoder.Close(); err != nil {
		ap.Logger.Warn(errorText + " Error closing output file: " + err.Error())
	}
}

func (ap *AudioProcessor) SaveConvolvers(filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := gob.NewEncoder(file)
	// This will automatically use your custom GobEncode methods
	return encoder.Encode(ap.Convolvers)
}

func (ap *AudioProcessor) LoadConvolvers(filename string, minTime string) error {
	cacheInfo, err := os.Stat(filename)

	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("cache file does not exist: %s", filename)
		}
		return err // Other file error
	}
	// Check if cache is too old
	modTime := cacheInfo.ModTime()
	cacheTime := modTime.UTC().Format(time.RFC3339)
	if cacheTime < minTime {
		return fmt.Errorf("cache is outdated: cache=%s, required=%s",
			cacheTime, minTime)
	}
	ap.Logger.Debug("LoadConvolvers" + " Cache OK proceeding to load...")
	file, err := os.Open(filename)
	if err != nil {
		return err // No backup exists - that's OK
	}
	defer file.Close()

	decoder := gob.NewDecoder(file)
	// This will automatically use your custom GobDecode methods
	return decoder.Decode(&ap.Convolvers)
}

// calculateExpiryTime returns the time when the cache will expire it is based off the remaining track time plus 10 seconds
func calculateExpiryTime(trackDuration float64, processingTime float64) int64 {
	remainingTrackTime := trackDuration - processingTime
	if remainingTrackTime < 0 {
		remainingTrackTime = 0
	}
	// Add 10 seconds to the remaining track time
	expiryDuration := time.Duration((remainingTrackTime + 10) * float64(time.Second))
	return time.Now().Add(expiryDuration).Unix()
}

func (ap *AudioProcessor) SaveDSPResiduals(Duration float64, processingTime float64) error {
	// Encode only the STATE (not the filter)
	convolverStates := make([][]byte, len(ap.Convolvers))
	for i, conv := range ap.Convolvers {
		state, err := conv.EncodeState() // Changed from GobEncode
		if err != nil {
			return err
		}
		convolverStates[i] = state
		//ap.Logger.Debug(fmt.Sprintf("Saving convolver ringbuffer position %d )", ap.Convolvers[i].RingPosition))
	}

	cache := DSPResidualsCache{
		Version:         CACHE_VERSION,
		SampleRate:      ap.Decoder.SampleRate,
		NumChannels:     ap.Decoder.NumChannels,
		ExpiryTime:      calculateExpiryTime(Duration, processingTime),
		ConvolverStates: convolverStates,
		DelayBuffers:    ap.Delay,
	}

	return ap.saveCacheToFile(cache)
}

func (ap *AudioProcessor) SaveDSPResidualsold(Duration float64, processingTime float64) error {
	// Encode each convolver's full state
	convolverStates := make([][]byte, len(ap.Convolvers))
	for i, conv := range ap.Convolvers {
		state, err := conv.GobEncode()
		if err != nil {
			return err
		}
		convolverStates[i] = state
	}

	cache := DSPResidualsCache{
		Version:         CACHE_VERSION, // ADD THIS!
		SampleRate:      ap.Decoder.SampleRate,
		NumChannels:     ap.Decoder.NumChannels,
		ExpiryTime:      calculateExpiryTime(Duration, processingTime),
		ConvolverStates: convolverStates, // Changed from tails/buffers
		DelayBuffers:    ap.Delay,
	}

	return ap.saveCacheToFile(cache)
}

// Keep the simple file helpers
func (ap *AudioProcessor) saveCacheToFile(cache interface{}) error {
	file, err := os.Create(ap.cachePath)
	if err != nil {
		return err
	}
	defer file.Close()
	return gob.NewEncoder(file).Encode(cache)
}

func (ap *AudioProcessor) loadCacheFromFile(filePath string) (*DSPResidualsCache, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var cache DSPResidualsCache
	if err := gob.NewDecoder(file).Decode(&cache); err != nil {
		return nil, err
	}
	return &cache, nil
}

// For RESIDUALS CACHE (dsp_residuals_*.gob) - state only
func (ap *AudioProcessor) LoadDSPResiduals() error {
	const functionName = "LoadDSPResiduals"
	errorText := fmt.Sprintf("%s:%s: ", packageName, functionName)
	cache, err := ap.loadCacheFromFile(ap.cachePath)
	if err != nil {
		return err
	}

	if cache.SampleRate != ap.Decoder.SampleRate ||
		cache.NumChannels != ap.Decoder.NumChannels ||
		time.Now().Unix() > cache.ExpiryTime {
		return fmt.Errorf("cache invalid")
	}
	if cache.Version != CACHE_VERSION {
		return fmt.Errorf("cache version mismatch: got %d, want %d",
			cache.Version, CACHE_VERSION)
	}

	ap.Delay = cache.DelayBuffers

	// Restore only the STATE (filter already loaded)
	for i := range ap.Convolvers {
		if i < len(cache.ConvolverStates) {
			err := ap.Convolvers[i].DecodeState(cache.ConvolverStates[i]) // Changed from GobDecode
			if err != nil {
				ap.Logger.Warn(errorText + fmt.Sprintf("Failed to restore convolver %d state: %v", i, err))
			}
		}
	}

	ap.Logger.Debug(errorText + "DSP Residuals loaded successfully")
	return nil
}

func (ap *AudioProcessor) LoadDSPResidualsold() error {
	const functionName = "LoadDSPResiduals"
	errorText := fmt.Sprintf("%s:%s: ", packageName, functionName)
	cache, err := ap.loadCacheFromFile(ap.cachePath)
	if err != nil {
		return err
	}

	// Simple validation
	if cache.SampleRate != ap.Decoder.SampleRate ||
		cache.NumChannels != ap.Decoder.NumChannels ||
		time.Now().Unix() > cache.ExpiryTime {
		return fmt.Errorf("cache invalid")
	}
	if cache.Version != CACHE_VERSION {
		return fmt.Errorf("cache version mismatch: got %d, want %d",
			cache.Version, CACHE_VERSION)
	}
	ap.Delay = cache.DelayBuffers

	// Restore full convolver state
	for i := range ap.Convolvers {
		if i < len(cache.ConvolverStates) {
			err := ap.Convolvers[i].GobDecode(cache.ConvolverStates[i])
			if err != nil {
				ap.Logger.Warn(errorText + fmt.Sprintf("Failed to restore convolver %d: %v", i, err))
				// Continue with other convolvers
			}
		}
	}

	ap.Logger.Debug(errorText + "DSP Residuals loaded successfully")
	return nil
}
