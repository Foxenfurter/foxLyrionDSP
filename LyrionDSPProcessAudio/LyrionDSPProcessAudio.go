package LyrionDSPProcessAudio

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"sync"
	"syscall"

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

	// Handle fatal errors with immediate exit
	fatalExit := func(err error, code int) {
		myLogger.Error(err.Error())
		exitCode = code
		os.Exit(code) // Immediate termination
	}

	// Detect if running in a pipe (LMS mode)
	isPipe := false
	if stat, _ := os.Stdout.Stat(); (stat.Mode() & os.ModeCharDevice) == 0 {
		isPipe = true
		myLogger.Debug("Data sourced from stdin")
	}

	// Buffer sizes
	const (
		decodedBuffer = 1
		channelBuffer = 2
		mergedBuffer  = 2
	)
	// original decoding
	//DecodedSamplesChannel := make(chan [][]float64, 10000)
	DecodedSamplesChannel := make(chan [][]float64, decodedBuffer)
	ErrorText := packageName + ":" + functionName + " Decoding Data..."
	myLogger.Debug(ErrorText)
	WG.Add(1)
	go func() {
		defer WG.Done()
		myDecoder.DecodeSamples(DecodedSamplesChannel, nil)

	}()

	// Create go channel for each audio channel
	audioChannels := make([]chan []float64, myDecoder.NumChannels)

	for i := range myDecoder.NumChannels {

		audioChannels[i] = make(chan []float64, channelBuffer)

	}
	ErrorText = packageName + ":" + functionName + " Splitting Channels... "
	myLogger.Debug(ErrorText)

	// Split audio data into separate channels
	channelSplitter(DecodedSamplesChannel, audioChannels, myDecoder.NumChannels, &WG, myLogger)

	// Apply convolution (FIR filter)
	convolvedChannels := make([]chan []float64, myDecoder.NumChannels)

	ErrorText = packageName + ":" + functionName + " Convolve Channels... "
	myLogger.Debug(ErrorText)
	for i := range myDecoder.NumChannels {
		convolvedChannels[i] = make(chan []float64, channelBuffer)
		applyConvolution(audioChannels[i], convolvedChannels[i], myConvolvers[i].FilterImpulse, &WG, myLogger)

	}

	mergedChannel := make(chan [][]float64, mergedBuffer)
	ErrorText = packageName + ":" + functionName + " Merge Channels... "
	myLogger.Debug(ErrorText)

	//go mergeChannels(audioChannels, mergedChannel, myEncoder.NumChannels, &WG, myLogger)
	go mergeChannels(convolvedChannels, mergedChannel, myEncoder.NumChannels, &WG, myLogger)

	ErrorText = packageName + ":" + functionName + " Encoding Data... "
	myLogger.Debug(ErrorText)
	WG.Add(1)
	go func() {

		defer func() {
			// Close the merged channel after encoding

			if exitCode == 0 {
				myLogger.Debug(packageName + ":" + functionName + " Finished Encoding...")
			}
			WG.Done()
		}()
		//err := myEncoder.EncodeSamplesChannel(DecodedSamplesChannel, nil)
		err := myEncoder.EncodeSamplesChannel(mergedChannel, nil)

		// new Error handling
		if err != nil {
			// Handle SIGPIPE/EPIPE explicitly & first
			if isPipe {
				switch {
				case errors.Is(err, syscall.EPIPE):
					fatalExit(errors.New("encoder: broken pipe (SIGPIPE) - output closed"), 2)
				case errors.Is(err, io.ErrClosedPipe):
					fatalExit(errors.New("encoder: output pipe closed prematurely"), 3)
				}
			}
			fatalExit(fmt.Errorf("encoder error: %w", err), 1)
		}

	}()

	ErrorText = packageName + ":" + functionName + " Waiting for procesing to complete..."
	myLogger.Debug(ErrorText)
	WG.Wait()
	/*err := myEncoder.Close()
	if err != nil {
		myLogger.Error(packageName + ":" + functionName + " Error closing output file: " + err.Error())
		// You might want to handle this error more explicitly
	}
	*/
}

func PeakDBFS(peak float64) float64 {
	if peak == 0 {
		return math.Inf(-1)
	}
	return 20 * math.Log10(peak)
}

// Split audio data into separate channels
func channelSplitter(inputCh chan [][]float64, outputChs []chan []float64, channelCount int, WG *sync.WaitGroup, myLogger *foxLog.Logger) {
	WG.Add(1) // Add to WaitGroup
	chunkCounter := 0
	go func() {
		defer WG.Done() // Mark as done when goroutine completes
		defer func() {  // Close all audio channels after splitting
			ErrorText := packageName + ":" + "Channel Splitter Done " +
				fmt.Sprintf("%d", channelCount) + " chunks " + fmt.Sprintf("%d", chunkCounter)

			for _, ch := range outputChs {
				close(ch)
			}
			myLogger.Debug(ErrorText)
		}()
		// a chunk is a systemFrame
		for chunk := range inputCh {
			for i := range channelCount {
				channelData := chunk[i]
				outputChs[i] <- channelData
			}
			chunkCounter++
		}

	}()
}

// Apply convolution (example: FIR filter)
func applyConvolution(inputCh, outputCh chan []float64, myImpulse []float64, WG *sync.WaitGroup, myLogger *foxLog.Logger) {
	const functionName = "applyConvolution"
	WG.Add(1) // Add to WaitGroup
	ErrorText := packageName + ":" + functionName + " Done... "
	go func() {
		defer func() {

			WG.Done() // Mark as done when goroutine completes
			//close(outputCh) // Critical closure
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

// Merge audio data from all channels
func mergeChannels(inputChannels []chan []float64, outputChannel chan [][]float64, numChannels int, WG *sync.WaitGroup, myLogger *foxLog.Logger) {
	const functionName = "mergeChannels"
	WG.Add(1) // Add to WaitGroup
	// Add a channel counter to track when all channels are closed
	allOk := 0
	activeChannels := make([]bool, numChannels)
	for i := range activeChannels {
		activeChannels[i] = true
	}
	go func() {
		defer func() {
			ErrorText := packageName + ":" + functionName + " Done..."
			myLogger.Debug(ErrorText)
			WG.Done() // Mark as done when goroutine completes
			close(outputChannel)
		}()

		for {
			mergedChunks := make([][]float64, numChannels) // Temporary slice to hold data from each channel

			for i := range numChannels {
				chunk, ok := <-inputChannels[i]
				if !ok {
					//inputChannels[i] = nil // Mark as closed
					if activeChannels[i] {
						activeChannels[i] = false
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

	}()

}
