package LyrionDSPFilters

import (
	"fmt"
	"math"
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

func BuildDSPFilters(myDecoder *foxAudioDecoder.AudioDecoder,
	myEncoder *foxAudioEncoder.AudioEncoder,
	myLogger *foxLog.Logger,
	myConfig *LyrionDSPSettings.ClientConfig) ([]foxConvolver.Convolver, error) {
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
				myLogger.Error(packageName + ": Error calculating filter: " + err.Error())
			}
		}
		myPEQ.GenerateFilterImpulse()
	}

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
		myResampler.FromSampleRate = myDecoder.SampleRate
		myResampler.ToSampleRate = myEncoder.SampleRate
		myResampler.Quality = 10

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

		myLogger.Debug(packageName + "Normalizing Data...")
		NormalizedChannel := make(chan [][]float64, 10000)

		WG.Add(1)
		go func() {
			defer WG.Done()
			outputSamples := make([][]float64, myDecoder.NumChannels)
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
			targetLevel := 0.71
			//some cleanup needed here
			targetLevel = foxNormalizer.TargetGainCSharp(myDecoder.SampleRate, myEncoder.SampleRate, targetLevel)
			myLogger.Debug(packageName + ": Target Gain:  " + fmt.Sprintf("%v", targetLevel))
			foxNormalizer.Normalize(outputSamples, targetLevel)
			NormalizedChannel <- outputSamples
			close(NormalizedChannel)
			myLogger.Debug(packageName + ": Outputting...")

		}()

		myLogger.Debug(packageName + ": Waiting...")
		WG.Wait()
		// so we now have an impulse from the fir file that is resampled and normalized
		// now we need to merge it with the peq impulse
		normalizedSamples := <-NormalizedChannel
		if applyPEQ { // by implication we also have a FIR impulse so we need to combine them
			myConvolvers = MergePEQandFIRFilters(&myPEQ, normalizedSamples)
		} else {
			myConvolvers = make([]foxConvolver.Convolver, myDecoder.NumChannels)
			for i := 0; i < len(myConvolvers); i++ {
				myConvolvers[i].FilterImpulse = normalizedSamples[i]
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
