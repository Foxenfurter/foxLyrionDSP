package LyrionDSPFilters

import (
	"fmt"
	"math"
	"os"
	"sync"

	"github.com/Foxenfurter/foxAudioLib/foxAudioDecoder"
	"github.com/Foxenfurter/foxAudioLib/foxAudioEncoder"
	"github.com/Foxenfurter/foxAudioLib/foxConvolver"
	"github.com/Foxenfurter/foxAudioLib/foxLog"
	"github.com/Foxenfurter/foxAudioLib/foxNormalizer"
	"github.com/Foxenfurter/foxAudioLib/foxPEQ"
	"github.com/Foxenfurter/foxAudioLib/foxResampler"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPSettings"
)

const packageName = "LyrionDSPFilters"

func LoadImpulse(inputFile string, outputFile string, targetSampleRate int, targetBitDepth int, myLogger *foxLog.Logger) ([][]float64, error) {
	const functionName = "LoadImpulse"
	const MsgHeader = packageName + ": " + functionName + ": "
	// Decode the audio file
	myFilterDecoder := foxAudioDecoder.AudioDecoder{
		Type: "WAV",
	}
	myFilterDecoder.Filename = inputFile
	impulseSamples := make([][]float64, 0)
	err := myFilterDecoder.Initialise()
	if err != nil {
		return nil, fmt.Errorf("%s: decoder init failed: %v", functionName, err)
	}

	// Calculate new size using sample rate and bit depth ratios
	sizeRatio := (float64(targetSampleRate) / float64(myFilterDecoder.SampleRate)) *
		(float64(targetBitDepth) / float64(myFilterDecoder.BitDepth))
	newSize := int64(float64(myFilterDecoder.Size) * sizeRatio)

	// Initialize Audio Encoder
	myFilterEncoder := foxAudioEncoder.AudioEncoder{
		Type:        "Wav",
		SampleRate:  targetSampleRate,
		BitDepth:    targetBitDepth, // Use targetBitDepth, not myDecoder.BitDepth
		NumChannels: myFilterDecoder.NumChannels,
		Size:        newSize,
		Filename:    outputFile,
	}

	myLogger.Debug(fmt.Sprintf(MsgHeader+" Output file: %s", myFilterEncoder.Filename))
	myLogger.Debug(fmt.Sprintf(MsgHeader+" Creating new Encoder SampleRate: %d Channels: %d BitDepth: %d", myFilterEncoder.SampleRate, myFilterEncoder.NumChannels, myFilterEncoder.BitDepth))

	err = myFilterEncoder.Initialise()
	if err != nil {
		myLogger.FatalError("Error Initialising Encoder: " + err.Error())
		os.Exit(1)
	}

	myResampler := foxResampler.NewResampler()
	myResampler.FromSampleRate = myFilterDecoder.SampleRate
	myResampler.ToSampleRate = targetSampleRate
	myResampler.Quality = 60
	myResampler.DebugOn = true
	myResampler.DebugFunc = myLogger.Debug
	var WG sync.WaitGroup
	DecodedSamplesChannel := make(chan [][]float64, 10000)
	ResampledChannel := make(chan [][]float64, 10000)

	myLogger.Debug(MsgHeader + " Decoding Data...")
	WG.Add(1)
	go func() {
		defer WG.Done()
		myFilterDecoder.DecodeSamples(DecodedSamplesChannel, nil)
		//close(DecodedSamplesChannel) // Close the channel after decoding
	}()

	WG.Add(1)
	go func() {
		defer WG.Done()
		minGains := make([]float64, 0)
		minGainLocations := make([]int, 0)
		for samples := range DecodedSamplesChannel {
			// Initialize to -1 to indicate no gain found yet
			if len(minGains) == 0 {
				minGains = make([]float64, len(samples))
				minGainLocations = make([]int, len(samples))
				for i := range minGainLocations {
					minGainLocations[i] = -1
					minGains[i] = 1.0
				}
			}
			for channelIndex, channelSamples := range samples {
				for sampleIndex, sample := range channelSamples {
					sampleAbs := math.Abs(sample)
					if sampleAbs < minGains[channelIndex] && sampleAbs != 0.0 {
						minGains[channelIndex] = sampleAbs
						minGainLocations[channelIndex] = sampleIndex
					}
				}
			}
			myResampler.InputSamples = samples
			err := myResampler.Resample()
			if err != nil {
				myLogger.Error(MsgHeader + " Resampling failed: " + err.Error())
				continue
			}
			ResampledChannel <- myResampler.InputSamples
		}

		myLogger.Debug(MsgHeader + "Max Gains: " + fmt.Sprintf("%v", minGains) + " Max Gain Locations: " + fmt.Sprintf("%v", minGainLocations))
		close(ResampledChannel) // Close the channel after resampling
	}()

	myLogger.Debug(MsgHeader + " Encoding Data...")
	WG.Add(1)
	go func() {
		defer WG.Done()
		outputSamples := make([][]float64, myFilterDecoder.NumChannels)
		for i := range outputSamples {
			outputSamples[i] = make([]float64, 0)
		}
		myLogger.Debug(MsgHeader + " Structure built now build output Samples...")
		for samples := range ResampledChannel {
			for channelIdx, channelData := range samples {
				outputSamples[channelIdx] = append(outputSamples[channelIdx], channelData...)
			}
		}
		myLogger.Debug(MsgHeader + " Ready to Normalize...")
		targetLevel := 0.89
		targetLevel = foxNormalizer.TargetGainCSharp(myResampler.FromSampleRate, myResampler.ToSampleRate, targetLevel)
		myLogger.Debug(MsgHeader + " Target Gain: " + fmt.Sprintf("%v", targetLevel))
		foxNormalizer.Normalize(outputSamples, targetLevel)
		myLogger.Debug(MsgHeader + " Outputting...")
		myFilterEncoder.EncodeData(outputSamples)
		impulseSamples = outputSamples
	}()

	myLogger.Debug(MsgHeader + " Waiting...")
	WG.Wait()

	return impulseSamples, nil
} // <-- ProcessAudio ends here

func BuildPEQFilter(
	myConfig *LyrionDSPSettings.ClientConfig,
	myAppSettings *LyrionDSPSettings.AppSettings, targetSampleRate int, myLogger *foxLog.Logger) (*foxPEQ.PEQFilter, error) {

	myPEQ := foxPEQ.NewPEQFilter(targetSampleRate, 15) // Create a single PEQFilter
	myPEQ.DebugFunc = myLogger.Debug

	// Apply filters if enabled and there are filters in config
	if len(myConfig.Filters) > 0 {
		for _, filter := range myConfig.Filters {
			err := myPEQ.CalcBiquadFilter(filter.FilterType, filter.Frequency, filter.Gain, filter.Slope, filter.SlopeType)
			// log error and continue - we will still generate an impulse
			if err != nil {
				myLogger.Warn(packageName + ": Error calculating filter: " + err.Error())
			}
		}
		myPEQ.GenerateFilterImpulse()
	}
	myLogger.Debug(packageName + ": PEQ Filter Built")
	return &myPEQ, nil
}

// CombineFilters - Combine the filter impulse with the PEQ impulse and handle appropriate scenarios where one, both or neither are present
func CombineFilters(filterImpulse [][]float64, myPEQ foxPEQ.PEQFilter, NumChannels int, myLogger *foxLog.Logger) ([]foxConvolver.Convolver, error) {
	// We are creating and returning a convolver for each channel
	myConvolvers := make([]foxConvolver.Convolver, NumChannels)
	myLogger.Debug(packageName + ": FIR Filter length: " + fmt.Sprintf(" %v", len(filterImpulse[0])))
	var applyPEQ bool
	if len(myPEQ.Impulse) == 0 {
		myLogger.Debug(packageName + ": No PEQ Filter")
		applyPEQ = false
	} else {
		myLogger.Debug(packageName + ": PEQ Filter")
		applyPEQ = true
	}

	if len(filterImpulse[0]) > 0 {

		// now we need to merge the normalized impulse with the PEQ impulse
		if applyPEQ { // by implication we also have a FIR impulse so we need to combine them

			myConvolvers = MergePEQandFIRFilters(&myPEQ, filterImpulse)
		} else {
			myLogger.Debug(packageName + ": No PEQ Filter - mapping FIR")
			myConvolvers = make([]foxConvolver.Convolver, NumChannels)
			for i := range myConvolvers {
				myConvolvers[i].FilterImpulse = filterImpulse[i]
			}
		}

	} else {
		myLogger.Debug(packageName + ": No FIR Filter - mapping PEQ")
		if applyPEQ {
			myConvolvers = make([]foxConvolver.Convolver, NumChannels)
			for i := range myConvolvers {
				myConvolvers[i].FilterImpulse = myPEQ.Impulse
			}
		}
	}

	return myConvolvers, nil

}

func BuildDSPFilters(myDecoder *foxAudioDecoder.AudioDecoder,
	myEncoder *foxAudioEncoder.AudioEncoder,
	myLogger *foxLog.Logger,
	myConfig *LyrionDSPSettings.ClientConfig,
	myAppSettings *LyrionDSPSettings.AppSettings) ([]foxConvolver.Convolver, error) {
	// Potentially we want to do this on a per channel basis, like this...
	applyPEQ := false

	myPEQ := foxPEQ.NewPEQFilter(myDecoder.SampleRate, 15) // Create a single PEQFilter
	myPEQ.DebugFunc = myLogger.Debug

	// Apply filters if enabled and there are filters in config
	if len(myConfig.Filters) > 0 {
		applyPEQ = true
		for _, filter := range myConfig.Filters {
			err := myPEQ.CalcBiquadFilter(filter.FilterType, filter.Frequency, filter.Gain, filter.Slope, filter.SlopeType)
			// log error and continue - we will still generate an impulse
			if err != nil {
				myLogger.Warn(packageName + ": Error calculating filter: " + err.Error())
			}
		}
		myPEQ.GenerateFilterImpulse()
	}
	outputSamples := make([][]float64, myDecoder.NumChannels)
	// No Merge needed without a FIR File
	myConvolvers := make([]foxConvolver.Convolver, myDecoder.NumChannels)
	if myConfig.FIRWavFile != "" {
		myImpulseDecoder := &foxAudioDecoder.AudioDecoder{
			Filename:  myConfig.FIRWavFile,
			Type:      "Wav",
			DebugFunc: myLogger.Debug,
		}

		err := myImpulseDecoder.Initialise()
		if err != nil {
			myLogger.Error(packageName + ": Error initializing AudioDecoder: " + err.Error())
		}

		myResampler := foxResampler.NewResampler()
		// resample the impulse response to match the source input file
		myResampler.FromSampleRate = myImpulseDecoder.SampleRate
		myResampler.ToSampleRate = myDecoder.SampleRate
		myResampler.Quality = 10
		myResampler.DebugOn = true
		myResampler.DebugFunc = myLogger.Debug
		myLogger.Debug(packageName + ": Resampling FIR impulse from " + fmt.Sprintf("%v", myResampler.FromSampleRate) + " to " + fmt.Sprintf("%v", myResampler.ToSampleRate))
		var WG sync.WaitGroup
		DecodedSamplesChannel := make(chan [][]float64, 10000)
		ResampledChannel := make(chan [][]float64, 10000)

		myLogger.Debug(packageName + ": Decoding FIR impulse...")
		WG.Add(1)
		go func() {
			defer WG.Done()
			myImpulseDecoder.DecodeSamples(DecodedSamplesChannel, nil)
			//close(DecodedSamplesChannel) // Close the channel after decoding
		}()

		WG.Add(1)
		go func() {
			defer WG.Done()
			minGains := make([]float64, 0)
			minGainLocations := make([]int, 0)
			for samples := range DecodedSamplesChannel {
				// Initialize to -1 to indicate no gain found yet
				if len(minGains) == 0 {
					minGains = make([]float64, len(samples))
					minGainLocations = make([]int, len(samples))
					for i := range minGainLocations {
						minGainLocations[i] = -1
						minGains[i] = 1.0
					}
				}
				for channelIndex, channelSamples := range samples {
					for sampleIndex, sample := range channelSamples {
						sampleAbs := math.Abs(sample)
						if sampleAbs < minGains[channelIndex] && sampleAbs != 0.0 {
							minGains[channelIndex] = sampleAbs
							minGainLocations[channelIndex] = sampleIndex
						}
					}
				}
				myResampler.InputSamples = samples
				err := myResampler.Resample()
				if err != nil {
					myLogger.Error(packageName + ": Resampling failed: " + err.Error())
					continue
				}
				ResampledChannel <- myResampler.InputSamples
			}
			myLogger.Debug(packageName + ": Max Gains: " + fmt.Sprintf("%v", minGains) + " Max Gain Locations: " + fmt.Sprintf("%v", minGainLocations))
			close(ResampledChannel) // Close the channel after resampling
		}()

		myLogger.Debug(packageName + "ormalizing Data...")

		WG.Add(1)
		go func() {
			defer WG.Done()

			for i := range outputSamples {
				outputSamples[i] = make([]float64, 0)
			}
			myLogger.Debug(packageName + ": Structure built now build output Samples...")
			for samples := range ResampledChannel {
				for channelIdx, channelData := range samples {
					outputSamples[channelIdx] = append(outputSamples[channelIdx], channelData...)
				}
			}
			myLogger.Debug(packageName + ": Ready to Normalize...")
			targetLevel := 0.89
			//some cleanup needed here
			targetLevel = foxNormalizer.TargetGainCSharp(myResampler.FromSampleRate, myResampler.ToSampleRate, targetLevel)
			myLogger.Debug(packageName + ": Target Gain:  " + fmt.Sprintf("%v", targetLevel))
			foxNormalizer.Normalize(outputSamples, targetLevel)

			myLogger.Debug(packageName + ": Outputting...")

		}()

		myLogger.Debug(packageName + ": Waiting...")
		WG.Wait()
		// so we now have an impulse from the fir file that is resampled and normalized
		// now we need to merge it with the peq impulse
		//normalizedSamples := outputSamples
		// Now write a copy of the filter
		targetBitDepth := 16
		sizeRatio := (float64(myResampler.ToSampleRate) / float64(myResampler.FromSampleRate)) *
			(float64(targetBitDepth) / float64(myDecoder.BitDepth))
		newSize := int64(float64(myDecoder.Size) * sizeRatio)

		myTempFirFilter := myAppSettings.TempDataFolder + "\\" + myConfig.Name + fmt.Sprintf("%d", myResampler.ToSampleRate) + ".wav"
		FirEncoder := &foxAudioEncoder.AudioEncoder{
			Type:        "WAV",
			SampleRate:  myResampler.ToSampleRate,
			BitDepth:    targetBitDepth,
			NumChannels: myDecoder.NumChannels,
			Size:        newSize,
			DebugFunc:   myLogger.Debug,
			DebugOn:     true,
			Filename:    myTempFirFilter,
		}
		err = FirEncoder.Initialise()
		if err != nil {
			myLogger.Error(packageName + "Error Initialising Audio Encoder: " + err.Error())
			return nil, err
		}
		FirEncoder.EncodeHeader()
		err = FirEncoder.EncodeData(outputSamples)
		if err != nil {
			myLogger.Error(packageName + "Error Encoding FIR Filter: " + err.Error())
			return nil, err
		}

		// now we need to merge the normalized impulse with the PEQ impulse
		if applyPEQ { // by implication we also have a FIR impulse so we need to combine them
			myConvolvers = MergePEQandFIRFilters(&myPEQ, outputSamples)
		} else {
			myConvolvers = make([]foxConvolver.Convolver, myDecoder.NumChannels)
			for i := 0; i < len(myConvolvers); i++ {
				myConvolvers[i].FilterImpulse = outputSamples[i]
			}
		}

	} else {
		myLogger.Debug(packageName + ": No FIR Filter - mapping PEQ")
		if applyPEQ {
			for i := range myConvolvers {
				myConvolvers[i].FilterImpulse = myPEQ.Impulse
			}
		}
	}

	return myConvolvers, nil
}

func MergePEQandFIRFilters(myPEQ *foxPEQ.PEQFilter,
	normalizedSamples [][]float64) []foxConvolver.Convolver {
	// At this point we have a single channel PEQ impulse and an n channel FIR impulse
	//let put in a loop vs myPEQ Impulse against normalizedSamples
	myConvolvers := make([]foxConvolver.Convolver, len(normalizedSamples))

	//println("Multi channel FIR filter match to corresponding channels")
	for i := range normalizedSamples {
		// cpoy the PEQ filter so that it is now the convolver filter
		myConvolvers[i].FilterImpulse = myPEQ.Impulse
		// and convolve it with the N normalized impulse
		myConvolvers[i].FilterImpulse = myConvolvers[i].ConvolveFFT(normalizedSamples[i])

		//some cleanup needed here

	}
	// We want to normalize the combined impulse Unfortunately it is one impulse per convolver, maybe design is wrong!
	targetLevel := 0.71
	// 1. Create a temporary array
	allImpulses := make([][]float64, len(myConvolvers))

	// 2. Populate the temporary array
	for i, convolver := range myConvolvers {
		allImpulses[i] = convolver.FilterImpulse
	}

	// 3. Normalize the temporary array
	foxNormalizer.Normalize(allImpulses, targetLevel)

	// 4. Copy normalized impulses back
	for i, impulse := range allImpulses {
		myConvolvers[i].FilterImpulse = impulse
	}

	return myConvolvers
}
