package LyrionDSPProcessAudio

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Foxenfurter/foxAudioLib/foxAudioDecoder"
	"github.com/Foxenfurter/foxAudioLib/foxAudioEncoder"
	"github.com/Foxenfurter/foxAudioLib/foxConvolver"
	"github.com/Foxenfurter/foxAudioLib/foxLog"
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
func ProcessAudio(myDecoder *foxAudioDecoder.AudioDecoder, myEncoder *foxAudioEncoder.AudioEncoder, myLogger *foxLog.Logger, myConfig *LyrionDSPSettings.ClientConfig, myConvolvers []foxConvolver.Convolver) {
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

	useBuffer := true
	// Buffer sizes
	const (
		decodedBuffer  = 1
		channelBuffer  = 2
		mergedBuffer   = 2
		feedbackBuffer = 1
	)
	// original decoding
	var (
		DecodedSamplesChannel chan [][]float64
		audioChannels         = make([]chan []float64, myDecoder.NumChannels)
		convolvedChannels     = make([]chan []float64, myDecoder.NumChannels)
		mergedChannel         chan [][]float64
		feedbackChannel       chan int64
	)

	if useBuffer {
		DecodedSamplesChannel = make(chan [][]float64, decodedBuffer)
		mergedChannel = make(chan [][]float64, mergedBuffer)
		feedbackChannel = make(chan int64, feedbackBuffer)
		for i := range len(audioChannels) {
			audioChannels[i] = make(chan []float64, channelBuffer)
			convolvedChannels[i] = make(chan []float64, channelBuffer)
		}
	}

	// Initialize channels
	os := runtime.GOOS

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
	go channelSplitter(DecodedSamplesChannel, audioChannels, myDecoder.NumChannels, &WG, myLogger)

	// 3. Convolution
	myLogger.Debug(ErrorText + " Setting up Channel Convolver...")
	for i := range myDecoder.NumChannels {
		myConvolver := foxConvolver.NewConvolver(myConvolvers[i].FilterImpulse)
		myConvolver.DebugOn = true
		myConvolver.DebugFunc = myLogger.Debug
		WG.Add(1)

		go func(ch int) {

			defer func() {
				close(convolvedChannels[ch])
				myLogger.Debug(fmt.Sprintf("Convolution channel %d closed", ch))
				WG.Done()
				myLogger.Debug(fmt.Sprintf("Convolution for channel %d done", ch))
			}()
			//applyConvolution(audioChannels[ch], convolvedChannels[ch], myConvolvers[ch].FilterImpulse, &WG, myLogger)
			myConvolver.ConvolveChannel(audioChannels[ch], convolvedChannels[ch])

		}(i)
	}

	// 4. Merging
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
		mergeChannels(convolvedChannels, mergedChannel, myDecoder.NumChannels, myLogger)

	}()

	// 5. Encoding (UNCHANGED)

	myLogger.Debug(ErrorText + " Setting up Encoder... ")
	WG.Add(1)
	go func() {
		defer func() {
			// Signal Done
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
			WG.Done() // Signal completion of decoding stage
			myLogger.Debug(ErrorText + " Decoding WG Done...")

		}()
		// 1A. Perform actual decoding
		myDecoder.DecodeSamples(DecodedSamplesChannel, feedbackChannel)

	}()

	myLogger.Debug(ErrorText + " Waiting for processing to complete...")

	WG.Wait()
	// Add explicit drain phase

	myLogger.Debug(ErrorText + "Processing Complete...")

	err := myEncoder.Close()
	if err != nil {
		myLogger.Error(ErrorText + " Error closing output file: " + err.Error())
		// You might want to handle this error more explicitly
	}

}

// Split audio data into separate channels
func channelSplitter(inputCh chan [][]float64, outputChs []chan []float64, channelCount int, WG *sync.WaitGroup, myLogger *foxLog.Logger) {

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

func mergeChannels(inputChannels []chan []float64, outputChannel chan [][]float64, numChannels int, myLogger *foxLog.Logger) {
	const functionName = "mergeChannels"
	defer myLogger.Debug(functionName + ": All channels drained")

	activeChannels := numChannels
	mergedChunks := make([][]float64, numChannels)

	for activeChannels > 0 {
		for i := 0; i < numChannels; i++ {
			select {
			case chunk, ok := <-inputChannels[i]:
				if !ok {
					inputChannels[i] = nil // Mark channel as closed
					activeChannels--
					myLogger.Debug(functionName + fmt.Sprintf(" : Input Channel %d closed", i))
					continue
				}
				mergedChunks[i] = chunk
			default:
				// Non-blocking check
			}
		}

		// Check if all channels in this iteration have data
		allReady := true
		for i := 0; i < numChannels; i++ {
			if inputChannels[i] != nil && mergedChunks[i] == nil {
				allReady = false
				break
			}
		}

		if allReady {
			outputChannel <- mergedChunks
			mergedChunks = make([][]float64, numChannels) // Reset
		}
	}
}

func mergeChannelsold(inputChannels []chan []float64, outputChannel chan [][]float64, numChannels int, myLogger *foxLog.Logger) {
	const functionName = "mergeChannels"
	defer myLogger.Debug("MERGER: All channels drained")
	// Add a channel counter to track when all channels are closed
	allOk := 0
	activeChannels := make([]bool, numChannels)
	for i := range activeChannels {
		activeChannels[i] = true
	}

	for {
		mergedChunks := make([][]float64, numChannels) // Temporary slice to hold data from each channel

		for i := range numChannels {
			chunk, ok := <-inputChannels[i]
			if !ok {
				//inputChannels[i] = nil // Mark as closed
				if activeChannels[i] {
					activeChannels[i] = false
					ErrorText := packageName + ":" + functionName + fmt.Sprintf(" channel %d complete...", i)
					myLogger.Debug(ErrorText)
					allOk += 1
				}

				//break
			} else {
				mergedChunks[i] = chunk
			}
		}

		if allOk == numChannels {
			ErrorText := packageName + ":" + functionName + " Merging channels complete... "
			myLogger.Debug(ErrorText)
			break
		}
		outputChannel <- mergedChunks
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
