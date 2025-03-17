package LyrionDSPProcessAudio

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"

	"github.com/Foxenfurter/foxAudioLib/foxAudioDecoder"
	"github.com/Foxenfurter/foxAudioLib/foxAudioEncoder"
	"github.com/Foxenfurter/foxAudioLib/foxConvolver"
	"github.com/Foxenfurter/foxAudioLib/foxLog"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPSettings"
)

const packageName = "LyrionDSPProcessAudio"

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
	if myArgs.InputFormat == "PCM" {
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
	myLogger.Debug("Initialising Encoder...")
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

	// now setup encoder
	// Initialize Audio Encoder

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
	// Detect if running in a pipe (LMS mode)
	isPipe := false
	if stat, _ := os.Stdout.Stat(); (stat.Mode() & os.ModeCharDevice) == 0 {
		isPipe = true
		myLogger.Debug("Running in pipe mode - enabling LMS safeguards")
	}

	// End of add pipe
	// original decoding
	//DecodedSamplesChannel := make(chan [][]float64, 10000)
	DecodedSamplesChannel := make(chan [][]float64, 10)
	//DecodedSamplesChannel := make(chan [][]float64, 2)
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

		audioChannels[i] = make(chan []float64)
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

		convolvedChannels[i] = make(chan []float64)
		applyConvolution(audioChannels[i], convolvedChannels[i], myConvolvers[i].FilterImpulse, &WG, myLogger)
	}

	mergedChannel := make(chan [][]float64)
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
			//close(mergedChannel)
			ErrorText = packageName + ":" + functionName + " Finished Encoding..."
			myLogger.Debug(ErrorText)
			WG.Done()
		}()
		//err := myEncoder.EncodeSamplesChannel(DecodedSamplesChannel, nil)
		err := myEncoder.EncodeSamplesChannel(mergedChannel, nil)

		// new Error handling
		if err != nil {
			// Handle SIGPIPE/EPIPE explicitly
			if isPipe {
				if errors.Is(err, syscall.EPIPE) {
					myLogger.Error("Encoder: Broken pipe (SIGPIPE) - output closed")
					return
				}

				if errors.Is(err, io.ErrClosedPipe) {
					myLogger.Error("Encoder: Output pipe closed prematurely")
					return
				}
			}
			myLogger.Error("Encoder error: " + err.Error())
		}
		// original error handling
		/*
			if err != nil {
				if errors.Is(err, io.ErrClosedPipe) {
					myLogger.Error("Encoder: Output pipe closed prematurely")
				} else {
					myLogger.Error("Encoder error: " + err.Error())
				}
			}
		*/

	}()

	ErrorText = packageName + ":" + functionName + " Waiting for procesing to complete..."
	myLogger.Debug(ErrorText)
	WG.Wait()

}

// Split audio data into separate channels
func channelSplitter(inputCh chan [][]float64, outputChs []chan []float64, channelCount int, WG *sync.WaitGroup, myLogger *foxLog.Logger) {
	WG.Add(1) // Add to WaitGroup
	chunkCounter := 0
	go func() {
		defer WG.Done() // Mark as done when goroutine completes
		defer func() {  // Close all audio channels after splitting
			ErrorText := packageName + ":" + "Channel Splitter Done " + fmt.Sprintf("%d", channelCount) + " chunks " + fmt.Sprintf("%d", chunkCounter)
			myLogger.Debug(ErrorText)
			for _, ch := range outputChs {
				close(ch)
			}

		}()

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
			myLogger.Debug(ErrorText)
			WG.Done() // Mark as done when goroutine completes
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
	ErrorText := packageName + ":" + functionName + " Done... "
	go func() {
		defer func() {
			myLogger.Debug(ErrorText)
			WG.Done() // Mark as done when goroutine completes
			close(outputChannel)
		}()

		for {
			mergedChunks := make([][]float64, numChannels) // Temporary slice to hold data from each channel
			allOk := true
			for i := range numChannels {
				chunk, ok := <-inputChannels[i]
				if !ok {
					//inputChannels[i] = nil // Mark as closed
					allOk = false
					//continue
					break
				}
				mergedChunks[i] = chunk

			}
			/*if len(mergedChunks) == 0 {
				break
			}*/
			if !allOk {
				ErrorText := packageName + ":" + functionName + " Merging channels complete... "
				myLogger.Debug(ErrorText)
				break
			}
			outputChannel <- mergedChunks
		}

	}()

}
