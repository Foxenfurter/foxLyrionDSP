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

	"github.com/Foxenfurter/foxAudioLib/foxAudioDecoder"
	"github.com/Foxenfurter/foxAudioLib/foxAudioEncoder"
	"github.com/Foxenfurter/foxAudioLib/foxConvolver"
	"github.com/Foxenfurter/foxAudioLib/foxLog"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPFilters"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPSettings"
)

const packageName = "LyrionDSPProcessAudio"

type AudioFrame struct {
	Sequence    int       // Global sequence number across all channels
	Channel     int       // Channel index (0-based)
	SampleCount int       // Number of samples in this frame
	Samples     []float64 // Actual sample data
}

// Initialize Audio Headers
func InitializeAudioHeaders(myArgs *LyrionDSPSettings.Arguments, myAppSettings *LyrionDSPSettings.AppSettings, myConfig *LyrionDSPSettings.ClientConfig, myLogger *foxLog.Logger) (foxAudioDecoder.AudioDecoder, foxAudioEncoder.AudioEncoder, error) {
	myDecoder := foxAudioDecoder.AudioDecoder{
		DebugFunc: myLogger.Debug,
		Type:      "WAV",
	}
	// If no input file is specified, use stdin by default
	if myArgs.InPath != "" {
		myDecoder.Filename = myArgs.InPath
	}
	myLogger.Debug("Input Format: " + myArgs.InputFormat)
	if strings.ToUpper(myArgs.InputFormat) == "PCM" {
		myDecoder.Type = "PCM"
		myDecoder.SampleRate = myArgs.InputSampleRate
		myDecoder.BitDepth = myArgs.PCMBits
		myDecoder.NumChannels = myArgs.PCMChannels
		myDecoder.BigEndian = myArgs.BigEndian
	} else {
		myDecoder.Type = "WAV"
	}

	err := myDecoder.Initialise()
	if err != nil {
		// initialise an empty encode purely for error handling
		myLogger.Error("Error Initialising Audio Decoder: " + err.Error())
		myEncoder := foxAudioEncoder.AudioEncoder{}
		return myDecoder, myEncoder, err
	}
	//myLogger.Debug("Initialising Encoder...")
	myEncoder := foxAudioEncoder.AudioEncoder{
		Type:        "WAV",
		SampleRate:  myDecoder.SampleRate,
		BitDepth:    myArgs.OutBits, // Use targetBitDepth, not myDecoder.BitDepth
		NumChannels: myDecoder.NumChannels,
		DebugFunc:   myLogger.Debug,
		DebugOn:     true,
		// use a file name here if there are issues with stdout
		//Filename:    "c:\\temp\\jonfile.wav",
	}
	if strings.ToUpper(myArgs.OutputFormat) == "PCM" {
		myEncoder.Type = "PCM"
	}
	if myDecoder.Size != 0 {
		myEncoder.Size = int64(myDecoder.Size) * int64(myArgs.OutBits) / int64(myDecoder.BitDepth) //outputSize = (inputSize * outputBitDepth) / inputBitDepth
	} else {
		myEncoder.Size = 0
	}

	if myArgs.OutPath != "" {
		myEncoder.Filename = myArgs.OutPath
	}
	err = myEncoder.Initialise()
	if err != nil {
		myLogger.Error("Error Initialising Audio Encoder: " + err.Error())
		return myDecoder, myEncoder, err
	}
	return myDecoder, myEncoder, nil
}

// function for processing audio through the DSP - this is where the main processing happens
func ProcessAudio(myDecoder *foxAudioDecoder.AudioDecoder,
	myEncoder *foxAudioEncoder.AudioEncoder,
	myLogger *foxLog.Logger,
	myConfig *LyrionDSPSettings.ClientConfig,
	myConvolvers []foxConvolver.Convolver,
	myAppSettings *LyrionDSPSettings.AppSettings,
	myArgs *LyrionDSPSettings.Arguments) {
	functionName := "ProcessAudio"
	var WG sync.WaitGroup
	//New Decoding
	exitCode := 0 // Track exit code for final exit
	ErrorText := packageName + ":" + functionName
	// Detect if running in a pipe (LMS mode)
	isPipe := false
	if stat, _ := os.Stdout.Stat(); (stat.Mode() & os.ModeCharDevice) == 0 {
		isPipe = true
		myLogger.Debug(ErrorText + ": Data sourced from stdin")
	}

	var err error
	useTail := true
	baseFileName := "convolver_" + myArgs.CleanClientID + "_tail.wav"
	myTailPath := filepath.Join(myAppSettings.TempDataFolder, baseFileName)
	myTailReader := new(foxAudioDecoder.AudioDecoder)
	myConvolverTail, err := myTailReader.LoadFiletoSampleBuffer(myTailPath, "WAV", myLogger)

	if err != nil {
		myLogger.Error(fmt.Sprintf("Error loading convolver tail: %v", err))
		useTail = false
	} else {
		if myTailReader.SampleRate != myDecoder.SampleRate || myTailReader.NumChannels != myDecoder.NumChannels {
			myLogger.Error(fmt.Sprintf("Error convolver tail sample rate %d does not match decoder sample rate %d", myTailReader.SampleRate, myDecoder.SampleRate))
			useTail = false
		} else {
			myLogger.Debug(fmt.Sprintf("Convolver tail loaded: %s, %d samples", myTailPath, len(myConvolverTail[0])))
		}
	}
	if !useTail {
		// we want to be able to output a tail even if we do not have one to load.
		myConvolverTail = make([][]float64, myDecoder.NumChannels)
	}
	// If we don't delete the tail file we may get a splurge of audio when we skip or restart.
	foxAudioEncoder.DeleteFile(myTailPath, myLogger)

	useBuffer := true
	// Initialize channels
	os := runtime.GOOS
	// Buffer sizes
	// getting dropouts after about 5 s on Pi at 192k, but increasing buffers makes no difference
	decodedBuffer := 1
	channelBuffer := 1
	mergedBuffer := 1

	feedbackBuffer := 1

	if os == "windows" {

		decodedBuffer = 1
		channelBuffer = 1
		mergedBuffer = 1
		feedbackBuffer = 1
	}

	// original decoding
	var (
		DecodedSamplesChannel chan [][]float64
		audioChannels         = make([]chan []float64, myDecoder.NumChannels)
		convolvedChannels     = make([]chan []float64, myDecoder.NumChannels)
		scaledChannels        = make([]chan []byte, myDecoder.NumChannels)
		finishedChannel       chan bool

		mergedChannel   chan [][]float64
		feedbackChannel chan int64
	)

	if useBuffer {
		DecodedSamplesChannel = make(chan [][]float64, decodedBuffer)
		mergedChannel = make(chan [][]float64, mergedBuffer)
		feedbackChannel = make(chan int64, feedbackBuffer)
		finishedChannel = make(chan bool, feedbackBuffer)

		for i := range len(audioChannels) {
			audioChannels[i] = make(chan []float64, channelBuffer)
			convolvedChannels[i] = make(chan []float64, channelBuffer)
			scaledChannels[i] = make(chan []byte, channelBuffer)
		}
	}

	if os != "windows" {
		feedbackChannel = nil
	}

	myLogger.Debug(ErrorText + ": Setup Audio Channels")

	// ================================================================
	// Start all RECEIVERS (downstream stages) FIRST
	// ================================================================

	// 2. Channel Splitting Create go channel for each audio channel

	myLogger.Debug(ErrorText + " Setting up channel splitter...")
	myDelay := LyrionDSPFilters.NewDelay(myDecoder.NumChannels, float64(myDecoder.SampleRate))
	myLogger.Debug(ErrorText + fmt.Sprintf(" peak at delays... %v", myConfig.Delay))
	switch delayMS := myConfig.Delay.Value; {
	case delayMS < 0:
		// delay Left
		myLogger.Debug(ErrorText + fmt.Sprintf(": Delay left channel %f ms", -delayMS))
		myDelay.AddDelay(0, -delayMS)
		break
	case delayMS > 0:
		//delay right
		myLogger.Debug(ErrorText + fmt.Sprintf(": Delay right channel %f ms", delayMS))
		myDelay.AddDelay(1, delayMS)
		break
	}

	WG.Add(1)
	// Split audio data into separate channels
	go channelSplitter(DecodedSamplesChannel, audioChannels, myDecoder.NumChannels, &WG, myLogger, myDelay)

	// 3. Convolution
	myLogger.Debug(ErrorText + " Setting up Channel Convolver...")

	for i := range myDecoder.NumChannels {
		// tried locking the OS thread but probably worse.
		//runtime.LockOSThread()
		myConvolver := foxConvolver.NewConvolver(myConvolvers[i].FilterImpulse)
		if useTail {
			myLogger.Debug("Convolver tail length: " + fmt.Sprintf("%d and Overlap length %d Channel %d",
				len(myConvolverTail[i]), len(myConvolver.FilterImpulse), i))
		} else {

			myLogger.Debug("No Convolver tail loaded")
		}
		myConvolver.DebugOn = true
		myConvolver.DebugFunc = myLogger.Debug
		WG.Add(1)

		go func(ch int) {

			defer func() {
				close(convolvedChannels[ch])
				myLogger.Debug(fmt.Sprintf("Convolution channel %d closed", ch))

				myConvolverTail[ch] = myConvolver.GetTail()

				WG.Done()
				myLogger.Debug(fmt.Sprintf("Convolution for channel %d done", ch))
			}()
			//applyConvolution(audioChannels[ch], convolvedChannels[ch], myConvolvers[ch].FilterImpulse, &WG, myLogger)
			myConvolver.ConvolveChannel(audioChannels[ch], convolvedChannels[ch])

		}(i)
	}

	// 5. Merge channels
	myLogger.Debug(ErrorText + " Setting up Channel Merger...")
	WG.Add(1)
	go func() {
		defer func() {
			close(mergedChannel)
			myLogger.Debug("Merge channel closed")
			WG.Done()
			// Explicit
			myLogger.Debug("Merge stage done...") // Log AFTER closure
		}()
		mergeChannels(convolvedChannels, mergedChannel, myDecoder.NumChannels, myConfig, myLogger)
	}()

	// 6. Encode Final output
	myLogger.Debug(ErrorText + " Setting up Encoder... ")
	WG.Add(1)
	go func() {
		defer func() {
			// Signal Done
			if finishedChannel != nil {
				finishedChannel <- true
			}
			WG.Done()
			myLogger.Debug(ErrorText + fmt.Sprintf(" Finished Encoding... %d", exitCode))
		}()
		//err := myEncoder.EncodeSamplesChannel(DecodedSamplesChannel, nil)
		err := myEncoder.EncodeSamplesChannel(mergedChannel, feedbackChannel)
		// new Error handling
		if err != nil {
			// Handle SIGPIPE/EPIPE explicitly & first
			if isPipe {
				switch {
				case errors.Is(err, syscall.EPIPE):
					myLogger.Error(ErrorText + " encoder: broken pipe (SIGPIPE) - output closed")
					exitCode = 2
					return
				case errors.Is(err, io.ErrClosedPipe):
					myLogger.Error(ErrorText + " encoder: output pipe closed prematurely")
					exitCode = 3
					return
				}
			}
			myLogger.Error(ErrorText + fmt.Errorf("encoder error: %w", err).Error())
			exitCode = 1
		}

	}()

	// 1. Decoding as this is the initiator it goes last -who'd a thunk it
	// In ProcessAudio function

	WG.Add(1)
	myLogger.Debug(ErrorText + " Decoding Data...")
	go func() {
		defer func() {
			//time.Sleep(200 * time.Millisecond)

			close(DecodedSamplesChannel)
			//time.Sleep(200 * time.Millisecond)

			myLogger.Debug(ErrorText + " Finished Decoding Data...")
			for {
				if feedbackChannel != nil {
					ws := <-feedbackChannel
					if ws == 0 {
						break
					}
					myLogger.Debug(ErrorText + "Feedback channel still open, Encoder has written " + fmt.Sprintf("%d", ws) + " samples...")
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
						myLogger.Debug(ErrorText + "Writer confirmed it has finished... ")
						break
					}
					myLogger.Debug(ErrorText + "Waiting for confirmation writer has finished... ")
					time.Sleep(200 * time.Millisecond)
				}
			}
			WG.Done() // Signal completion of decoding stage
			myLogger.Debug(ErrorText + fmt.Sprintf(" Decoding WG Done... and writer finished %t", writerFinished))

		}()
		// 1A. Perform actual decoding
		myDecoder.DecodeSamples(DecodedSamplesChannel, feedbackChannel)

	}()

	myLogger.Debug(ErrorText + " Waiting for processing to complete...")

	WG.Wait()
	if len(myConvolverTail[0]) > 0 {
		// Backup Convolver Tail

		targetBitDepth := 32
		myLogger.Debug("Backup Tail: " + myTailPath)
		foxAudioEncoder.WriteWavFile(
			myTailPath,
			myConvolverTail,
			myDecoder.SampleRate,
			targetBitDepth,
			len(myConvolverTail),
			true, // overwrite
			myLogger,
		)
	} else {
		myLogger.Debug("No tail to backup")
	}
	// Add explicit drain phase

	myLogger.Debug(ErrorText + " Processing Complete...")

	err = myEncoder.Close()
	if err != nil {
		myLogger.Error(ErrorText + " Error closing output file: " + err.Error())
		// You might want to handle this error more explicitly
	}

}

// Splits channels for parallel convolution and adds delays if they have been specificed
func channelSplitter(
	inputCh chan [][]float64,
	outputChs []chan []float64,
	channelCount int,
	WG *sync.WaitGroup,
	myLogger *foxLog.Logger,
	delay *LyrionDSPFilters.Delay,
) {
	chunkCounter := 0
	go func() {
		defer func() {
			// Flush remaining data in buffers
			for i := 0; i < channelCount; i++ {
				if len(delay.Buffers[i]) > 0 {
					outputChs[i] <- delay.Buffers[i]
				}
				close(outputChs[i])
				myLogger.Debug(packageName + fmt.Sprintf("splitter closed channel %d", i))
			}
			WG.Done()
			myLogger.Debug(packageName + ":Channel Splitter Done... " +
				fmt.Sprintf("%d chunks processed", chunkCounter))
		}()

		for chunk := range inputCh {
			for i := 0; i < channelCount; i++ {
				channelData := chunk[i]
				if delay.Delays[i] > 0 {
					// Prepend buffer to current data
					combined := append(delay.Buffers[i], channelData...)

					// Output the first part (length = chunk size)
					outputLength := len(channelData)
					if outputLength > len(combined) {
						outputLength = len(combined)
					}
					outputData := combined[:outputLength]
					outputChs[i] <- outputData

					// Retain last `delay.delays[i]` samples for next iteration
					retainStart := len(combined) - delay.Delays[i]
					if retainStart < 0 {
						retainStart = 0
					}
					delay.Buffers[i] = combined[retainStart:]
				} else {
					outputChs[i] <- channelData // No delay
				}
			}
			chunkCounter++
		}
	}()
}

// Split audio data into separate channels
func channelSplitterBasic(inputCh chan [][]float64, outputChs []chan []float64, channelCount int, WG *sync.WaitGroup, myLogger *foxLog.Logger) {

	chunkCounter := 0
	go func() {
		//defer WG.Done() // Mark as done when goroutine completes
		defer func() { // Close all audio channels after splitting

			for i, ch := range outputChs {
				close(ch)
				myLogger.Debug(packageName + fmt.Sprintf("splitter closed channel %d", i))
			}
			WG.Done()
			ErrorText := packageName + ":" + "Channel Splitter Done... " +
				fmt.Sprintf("%d", channelCount) + " chunks " + fmt.Sprintf("%d", chunkCounter)
			myLogger.Debug(ErrorText)
		}()
		// a chunk is a systemFrame
		for chunk := range inputCh {
			//myLogger.Debug("channel splitter - sending chunk")
			for i := range channelCount {
				channelData := chunk[i]
				outputChs[i] <- channelData
			}
			chunkCounter++
		}

	}()
}

func mergeByteChannels(inputChannels []chan []byte, outputChannel chan []byte, numChannels int, bitDepth int, myLogger *foxLog.Logger) {
	const functionName = "mergeChannels"
	defer myLogger.Debug(functionName + ": All channels drained")

	bytesPerSample := bitDepth / 8
	mergedChunks := make([][]byte, numChannels)
	closed := make([]bool, numChannels)
	activeChannels := numChannels

	for activeChannels > 0 {
		// Non-blocking check for data (like mergeChannels)
		for i := 0; i < numChannels; i++ {
			if closed[i] {
				continue
			}

			select {
			case chunk, ok := <-inputChannels[i]:
				if !ok {
					closed[i] = true
					activeChannels--
					myLogger.Debug(fmt.Sprintf("%s: Channel %d closed", functionName, i))
					continue
				}
				mergedChunks[i] = chunk
			default:
				// Non-blocking: Skip if no data
			}
		}

		// Check if all active channels have data (like mergeChannels)
		allReady := true
		var chunkLength int
		for i := 0; i < numChannels; i++ {
			if !closed[i] && mergedChunks[i] == nil {
				allReady = false
				break
			}
			if !closed[i] && chunkLength == 0 {
				chunkLength = len(mergedChunks[i])
			}
		}

		if allReady && chunkLength > 0 {
			// Interleave and send
			interleaved := make([]byte, chunkLength*numChannels)
			for sample := 0; sample < chunkLength/bytesPerSample; sample++ {
				for ch := 0; ch < numChannels; ch++ {
					if closed[ch] || mergedChunks[ch] == nil {
						continue
					}
					srcPos := sample * bytesPerSample
					destPos := (sample*numChannels + ch) * bytesPerSample
					copy(interleaved[destPos:], mergedChunks[ch][srcPos:srcPos+bytesPerSample])
				}
			}
			outputChannel <- interleaved
			mergedChunks = make([][]byte, numChannels) // Reset
		}
	}
}

func mergeChannels(inputChannels []chan []float64, outputChannel chan [][]float64, numChannels int, myConfig *LyrionDSPSettings.ClientConfig, myLogger *foxLog.Logger) {
	const functionName = "mergeChannels"
	defer myLogger.Debug(functionName + ": All channels drained")

	channelGains := LyrionDSPFilters.GetChannelsGain(myConfig, numChannels, myLogger)
	if numChannels > 1 {
		myLogger.Debug(functionName + fmt.Sprintf(" : Setup, channel gain left %f, right %f", channelGains[0], channelGains[1]))
	}

	var sigmaGain, deltaGain float64
	var applyWidth bool
	if myConfig.Width != 0.0 && numChannels > 1 {
		applyWidth = true
		sigmaGain, deltaGain = LyrionDSPFilters.GetWidthCoefficients(myConfig.Width)
		myLogger.Debug(functionName + fmt.Sprintf(" : Setup, width delta %f, sigma %f", deltaGain, sigmaGain))
	}

	activeChannels := numChannels
	var mergedChunks [][]float64

	for activeChannels > 0 {
		// Reset mergedChunks for new data
		mergedChunks = make([][]float64, numChannels)

		// Read from all channels, blocking until data is available
		for i := range numChannels {
			if inputChannels[i] == nil {
				continue // Channel closed
			}

			chunk, ok := <-inputChannels[i]
			if !ok {
				inputChannels[i] = nil
				activeChannels--
				myLogger.Debug(functionName + fmt.Sprintf(" : Input Channel %d closed", i))
				continue
			}

			// Apply gain
			for j := range chunk {
				chunk[j] *= channelGains[i]
			}
			mergedChunks[i] = chunk
		}

		// Check if all active channels have data
		allReady := true
		for i := range numChannels {
			if inputChannels[i] != nil && mergedChunks[i] == nil {
				allReady = false
				break
			}
		}

		if allReady {
			if applyWidth {
				for mc := range mergedChunks[0] {
					mid := (mergedChunks[0][mc] + mergedChunks[1][mc]) * sigmaGain
					side := (mergedChunks[0][mc] - mergedChunks[1][mc]) * deltaGain
					mergedChunks[0][mc] = mid + side
					mergedChunks[1][mc] = mid - side
				}
			}
			// Send the processed chunks
			outputChannel <- mergedChunks
		}
	}
}

func PeakDBFS(peak float64) float64 {
	if peak == 0 {
		return math.Inf(-1)
	}
	return 20 * math.Log10(peak)
}

func isChannelClosed(ch interface{}) bool {
	switch c := ch.(type) {
	case chan [][]float64:
		select {
		case _, ok := <-c:
			return !ok
		default:
			return false
		}
	case chan []float64:
		select {
		case _, ok := <-c:
			return !ok
		default:
			return false
		}
	default:
		return true
	}
}

func areChannelsClosed(chs []chan []float64) bool {
	for _, ch := range chs {
		if !isChannelClosed(ch) {
			return false
		}
	}
	return true
}

// Apply convolution (example: FIR filter)
func applyConvolution(inputCh, outputCh chan []float64, myImpulse []float64, WG *sync.WaitGroup, myLogger *foxLog.Logger) {
	const functionName = "applyConvolution"
	WG.Add(1) // Add to WaitGroup
	ErrorText := packageName + ":" + functionName + " Done... "
	go func() {
		defer func() {

			WG.Done()       // Mark as done when goroutine completes
			close(outputCh) // Critical closure
			myLogger.Debug(ErrorText)
		}()
		myConvolver := foxConvolver.NewConvolver(myImpulse)
		if myLogger.DebugEnabled {
			myConvolver.DebugOn = true
			myConvolver.DebugFunc = myLogger.Debug
		}

		myConvolver.ConvolveChannel(inputCh, outputCh)

	}()
}

func ProcessAudioOld(myDecoder *foxAudioDecoder.AudioDecoder,
	myEncoder *foxAudioEncoder.AudioEncoder,
	myLogger *foxLog.Logger,
	myConfig *LyrionDSPSettings.ClientConfig,
	myConvolvers []foxConvolver.Convolver,
	myAppSettings *LyrionDSPSettings.AppSettings,
	myArgs *LyrionDSPSettings.Arguments) {
	functionName := "ProcessAudio"
	var WG sync.WaitGroup
	//New Decoding
	exitCode := 0 // Track exit code for final exit
	ErrorText := packageName + ":" + functionName
	// Detect if running in a pipe (LMS mode)
	isPipe := false
	if stat, _ := os.Stdout.Stat(); (stat.Mode() & os.ModeCharDevice) == 0 {
		isPipe = true
		myLogger.Debug(ErrorText + ": Data sourced from stdin")
	}
	// load convolver tail, must exist and must have same sample rate as signal
	//var myConvolverTail [][]float64
	var err error
	useTail := true
	baseFileName := "convolver_" + myArgs.CleanClientID + "_tail.wav"
	myTailPath := filepath.Join(myAppSettings.TempDataFolder, baseFileName)
	myTailReader := new(foxAudioDecoder.AudioDecoder)
	myConvolverTail, err := myTailReader.LoadFiletoSampleBuffer(myTailPath, "WAV", myLogger)

	if err != nil {
		myLogger.Error(fmt.Sprintf("Error loading convolver tail: %v", err))
		useTail = false
	} else {
		if myTailReader.SampleRate != myDecoder.SampleRate || myTailReader.NumChannels != myDecoder.NumChannels {
			myLogger.Error(fmt.Sprintf("Error convolver tail sample rate %d does not match decoder sample rate %d", myTailReader.SampleRate, myDecoder.SampleRate))
			useTail = false
		} else {
			myLogger.Debug(fmt.Sprintf("Convolver tail loaded: %s, %d samples", myTailPath, len(myConvolverTail[0])))
		}
	}
	if !useTail {
		// we want to be able to output a tail even if we do not have one to load.
		myConvolverTail = make([][]float64, myDecoder.NumChannels)
	}
	// If we don't delete the tail file we may get a splurge of audio when we skip or restart.
	foxAudioEncoder.DeleteFile(myTailPath, myLogger)

	newProcess := false
	useBuffer := true
	// Initialize channels
	os := runtime.GOOS
	// Buffer sizes
	// getting dropouts after about 5 s on Pi at 192k, but increasing buffers makes no difference
	decodedBuffer := 1
	channelBuffer := 1
	mergedBuffer := 1

	feedbackBuffer := 1

	if os == "windows" {

		decodedBuffer = 1
		channelBuffer = 1
		mergedBuffer = 1
		feedbackBuffer = 1
	}

	// original decoding
	var (
		DecodedSamplesChannel chan [][]float64
		audioChannels         = make([]chan []float64, myDecoder.NumChannels)
		convolvedChannels     = make([]chan []float64, myDecoder.NumChannels)
		scaledChannels        = make([]chan []byte, myDecoder.NumChannels)
		finishedChannel       chan bool
		mergedByteChannel     chan []byte
		mergedChannel         chan [][]float64
		feedbackChannel       chan int64
	)

	if useBuffer {
		DecodedSamplesChannel = make(chan [][]float64, decodedBuffer)
		mergedChannel = make(chan [][]float64, mergedBuffer)
		feedbackChannel = make(chan int64, feedbackBuffer)
		finishedChannel = make(chan bool, feedbackBuffer)
		mergedByteChannel = make(chan []byte, mergedBuffer)
		for i := range len(audioChannels) {
			audioChannels[i] = make(chan []float64, channelBuffer)
			convolvedChannels[i] = make(chan []float64, channelBuffer)
			scaledChannels[i] = make(chan []byte, channelBuffer)
		}
	}

	if os != "windows" {
		feedbackChannel = nil
	}

	myLogger.Debug(ErrorText + ": Setup Audio Channels")

	// ================================================================
	// Start all RECEIVERS (downstream stages) FIRST
	// ================================================================

	// 2. Channel Splitting Create go channel for each audio channel

	myLogger.Debug(ErrorText + " Setting up channel splitter...")
	WG.Add(1)
	// Split audio data into separate channels
	go channelSplitterBasic(DecodedSamplesChannel, audioChannels, myDecoder.NumChannels, &WG, myLogger)

	// 3. Convolution
	myLogger.Debug(ErrorText + " Setting up Channel Convolver...")

	for i := range myDecoder.NumChannels {
		// tried locking the OS thread but probably worse.
		//runtime.LockOSThread()
		myConvolver := foxConvolver.NewConvolver(myConvolvers[i].FilterImpulse)
		if useTail {
			myLogger.Debug("Convolver tail length: " + fmt.Sprintf("%d and Overlap length %d Channel %d",
				len(myConvolverTail[i]), len(myConvolver.FilterImpulse), i))
		} else {

			myLogger.Debug("No Convolver tail loaded")
		}
		myConvolver.DebugOn = true
		myConvolver.DebugFunc = myLogger.Debug
		WG.Add(1)

		go func(ch int) {

			defer func() {
				close(convolvedChannels[ch])
				myLogger.Debug(fmt.Sprintf("Convolution channel %d closed", ch))

				myConvolverTail[ch] = myConvolver.GetTail()

				WG.Done()
				myLogger.Debug(fmt.Sprintf("Convolution for channel %d done", ch))
			}()
			//applyConvolution(audioChannels[ch], convolvedChannels[ch], myConvolvers[ch].FilterImpulse, &WG, myLogger)
			myConvolver.ConvolveChannel(audioChannels[ch], convolvedChannels[ch])

		}(i)
	}

	if newProcess {
		// 4. Scale and Dither - Set Bit Depth and Dither
		myLogger.Debug(ErrorText + " Setting up Channel Dither and Scaling...")
		// Add a WaitGroup for scaling goroutines
		scaleWG := sync.WaitGroup{}
		WG.Add(1)
		for i := range myDecoder.NumChannels {
			scaleWG.Add(1)

			go func(ch int) {

				defer func() {
					scaleWG.Done()
					myLogger.Debug(fmt.Sprintf("Scaling for channel %d done", ch))

				}()
				// Perform Dithering and then BitDepth conversion
				for convSamples := range convolvedChannels[ch] {
					mybytesBuffer, err := myEncoder.EncodeSingleChannel(convSamples)

					if err != nil {
						myLogger.Error(fmt.Sprintf("Error Scaling and Dithering channel %d: %v", ch, err))
						return
					}
					scaledChannels[ch] <- mybytesBuffer

				}

			}(i)
		}
		// Close all scaledChannels after ALL scaling goroutines finish
		go func() {
			scaleWG.Wait()
			for ch := range scaledChannels {
				close(scaledChannels[ch])
				myLogger.Debug(fmt.Sprintf("Scaling channel %d closed", ch))
			}
			myEncoder.Peak = myEncoder.Encoder.GetPeak()
			WG.Done()
			myLogger.Debug("All scaling done...")
		}()

		// 5. Merging
		myLogger.Debug(ErrorText + " Setting up Byte Merger...")

		WG.Add(1)
		go func() {
			defer func() {
				close(mergedChannel)
				myLogger.Debug("Byte Merge channel closed")
				WG.Done()
				// Explicit
				myLogger.Debug("Byte Merge stage done...") // Log AFTER closure
			}()
			mergeByteChannels(scaledChannels, mergedByteChannel, myDecoder.NumChannels, myEncoder.BitDepth, myLogger)

		}()

		// 6. Writing (New)

		myLogger.Debug(ErrorText + " Setting up Writer... ")

		WG.Add(1)
		go func() {
			defer func() {
				myLogger.Debug(ErrorText + fmt.Sprintf(" Writing Sending Completion... %d", exitCode))
				// Signal Done
				if finishedChannel != nil {
					finishedChannel <- true
				}
				WG.Done()
				myLogger.Debug(ErrorText + fmt.Sprintf(" Writing Done... %d", exitCode))
			}()
			//err := myEncoder.EncodeSamplesChannel(DecodedSamplesChannel, nil)
			err := myEncoder.WriteBytesChannel(mergedByteChannel, feedbackChannel)
			// new Error handling
			if err != nil {
				// Handle SIGPIPE/EPIPE explicitly & first
				if isPipe {
					switch {
					case errors.Is(err, syscall.EPIPE):
						myLogger.Error(ErrorText + " encoder: broken pipe (SIGPIPE) - output closed")
						exitCode = 2
						return
					case errors.Is(err, io.ErrClosedPipe):
						myLogger.Error(ErrorText + " encoder: output pipe closed prematurely")
						exitCode = 3
						return
					}
				}
				myLogger.Error(ErrorText + fmt.Errorf(" encoder error: %w", err).Error())
				exitCode = 1
			}

		}()
	} else {
		//****************OLD PROCESS *************************
		// 5. Merging-old
		myLogger.Debug(ErrorText + " Setting up Channel Merger...")
		WG.Add(1)
		go func() {
			defer func() {
				close(mergedChannel)
				myLogger.Debug("Merge channel closed")
				WG.Done()
				// Explicit
				myLogger.Debug("Merge stage done...") // Log AFTER closure
			}()
			mergeChannels(convolvedChannels, mergedChannel, myDecoder.NumChannels, myConfig, myLogger)
		}()

		// 6. Encoding (UNCHANGED)
		myLogger.Debug(ErrorText + " Setting up Encoder... ")
		WG.Add(1)
		go func() {
			defer func() {
				// Signal Done
				if finishedChannel != nil {
					finishedChannel <- true
				}
				WG.Done()
				myLogger.Debug(ErrorText + fmt.Sprintf(" Finished Encoding... %d", exitCode))
			}()
			//err := myEncoder.EncodeSamplesChannel(DecodedSamplesChannel, nil)
			err := myEncoder.EncodeSamplesChannel(mergedChannel, feedbackChannel)
			// new Error handling
			if err != nil {
				// Handle SIGPIPE/EPIPE explicitly & first
				if isPipe {
					switch {
					case errors.Is(err, syscall.EPIPE):
						myLogger.Error(ErrorText + " encoder: broken pipe (SIGPIPE) - output closed")
						exitCode = 2
						return
					case errors.Is(err, io.ErrClosedPipe):
						myLogger.Error(ErrorText + " encoder: output pipe closed prematurely")
						exitCode = 3
						return
					}
				}
				myLogger.Error(ErrorText + fmt.Errorf("encoder error: %w", err).Error())
				exitCode = 1
			}

		}()
	}

	// 1. Decoding as this is the initiator it goes last -who'd a thunk it
	// In ProcessAudio function

	WG.Add(1)
	myLogger.Debug(ErrorText + " Decoding Data...")
	go func() {
		defer func() {
			//time.Sleep(200 * time.Millisecond)

			close(DecodedSamplesChannel)
			//time.Sleep(200 * time.Millisecond)

			myLogger.Debug(ErrorText + " Finished Decoding Data...")
			for {
				if feedbackChannel != nil {
					ws := <-feedbackChannel
					if ws == 0 {
						break
					}
					myLogger.Debug(ErrorText + "Feedback channel still open, Encoder has written " + fmt.Sprintf("%d", ws) + " samples...")
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
						myLogger.Debug(ErrorText + "Writer confirmed it has finished... ")
						break
					}
					myLogger.Debug(ErrorText + "Waiting for confirmation writer has finished... ")
					time.Sleep(200 * time.Millisecond)
				}
			}
			WG.Done() // Signal completion of decoding stage
			myLogger.Debug(ErrorText + fmt.Sprintf(" Decoding WG Done... and writer finished %t", writerFinished))

		}()
		// 1A. Perform actual decoding
		myDecoder.DecodeSamples(DecodedSamplesChannel, feedbackChannel)

	}()

	myLogger.Debug(ErrorText + " Waiting for processing to complete...")

	WG.Wait()
	if len(myConvolverTail[0]) > 0 {
		// Backup Convolver Tail

		targetBitDepth := 32
		myLogger.Debug("Backup Tail: " + myTailPath)
		foxAudioEncoder.WriteWavFile(
			myTailPath,
			myConvolverTail,
			myDecoder.SampleRate,
			targetBitDepth,
			len(myConvolverTail),
			true, // overwrite
			myLogger,
		)
	} else {
		myLogger.Debug("No tail to backup")
	}
	// Add explicit drain phase

	myLogger.Debug(ErrorText + " Processing Complete...")

	err = myEncoder.Close()
	if err != nil {
		myLogger.Error(ErrorText + " Error closing output file: " + err.Error())
		// You might want to handle this error more explicitly
	}

}
