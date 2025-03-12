package LyrionDSPProcessAudio

import (
	"fmt"
	"sync"

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

	DecodedSamplesChannel := make(chan [][]float64, 10000)
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
		applyConvolution(audioChannels[i], convolvedChannels[i], myConvolvers[i].FilterImpulse, &WG)
	}

	mergedChannel := make(chan [][]float64)
	ErrorText = packageName + ":" + functionName + " Merge Channels... "
	myLogger.Debug(ErrorText)

	//go mergeChannels(audioChannels, mergedChannel, myEncoder.NumChannels, &WG)
	go mergeChannels(convolvedChannels, mergedChannel, myEncoder.NumChannels, &WG)

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
		if err != nil {
			myLogger.Error(ErrorText + err.Error())
		}

	}()

	ErrorText = packageName + ":" + functionName + " Waiting for procesing to complete..."
	myLogger.Debug(ErrorText)
	WG.Wait()

}

// Split audio data into separate channels
func channelSplitter(inputCh chan [][]float64, outputChs []chan []float64, channelCount int, WG *sync.WaitGroup, myLogger *foxLog.Logger) {
	//	WG.Add(1) // Add to WaitGroup
	go func() {
		//		defer WG.Done() // Mark as done when goroutine completes
		defer func() { // Close all audio channels after splitting
			for _, ch := range outputChs {
				close(ch)
			}
		}()
		chunkCounter := 0
		for chunk := range inputCh {
			for i := range channelCount {
				channelData := chunk[i]
				outputChs[i] <- channelData
			}
			chunkCounter++
		}
		ErrorText := packageName + ":" + "Channel Splitter Done " + fmt.Sprintf("%d", channelCount) + " chunks " + fmt.Sprintf("%d", chunkCounter)
		myLogger.Debug(ErrorText)

	}()
}

// Apply convolution (example: FIR filter)
func applyConvolution(inputCh, outputCh chan []float64, myImpulse []float64, WG *sync.WaitGroup) {

	//WG.Add(1) // Add to WaitGroup
	go func() {
		//defer WG.Done() // Mark as done when goroutine completes
		myConvolver := foxConvolver.NewConvolver(myImpulse)
		myConvolver.ConvolveChannel(inputCh, outputCh)

	}()
}

// Merge audio data from all channels
func mergeChannels(inputChannels []chan []float64, outputChannel chan [][]float64, numChannels int, WG *sync.WaitGroup) {
	//WG.Add(1) // Add to WaitGroup
	go func() {
		//	defer WG.Done() // Mark as done when goroutine completes
		defer func() { // Close the output channel after merging
			close(outputChannel)
		}()
		for {
			var mergedChunks [][]float64 // Temporary slice to hold data from each channel
			for i := range numChannels {
				chunk, ok := <-inputChannels[i]
				if !ok {
					inputChannels[i] = nil // Mark as closed
					continue
				}
				mergedChunks = append(mergedChunks, chunk)
			}
			if len(mergedChunks) == 0 {
				break
			}
			outputChannel <- mergedChunks
		}

	}()

}
