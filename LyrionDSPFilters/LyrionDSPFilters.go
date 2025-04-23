package LyrionDSPFilters

import (
	"fmt"
	"math"
	"math/rand"

	"os"
	"sync"

	"github.com/Foxenfurter/foxAudioLib/foxAudioDecoder"
	"github.com/Foxenfurter/foxAudioLib/foxConvolver"
	"github.com/Foxenfurter/foxAudioLib/foxLog"
	"github.com/Foxenfurter/foxAudioLib/foxNormalizer"
	"github.com/Foxenfurter/foxAudioLib/foxPEQ"
	"github.com/Foxenfurter/foxAudioLib/foxResampler"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPSettings"
)

const packageName = "LyrionDSPFilters"

func LoadImpulse(inputFile string, targetSampleRate int, targetLevel float64, myLogger *foxLog.Logger) ([][]float64, error) {
	const functionName = "LoadImpulse"
	const MsgHeader = packageName + ": " + functionName + ": "
	myLogger.Debug(MsgHeader + " Loading impulse...")

	if _, err := os.Stat(inputFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("input file %s does not exist", inputFile)
	}
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
	myLogger.Debug(MsgHeader + fmt.Sprintf("Impulse Decoder initialized: SampleRate=%d, Channels=%d, Type=%s", myFilterDecoder.SampleRate, myFilterDecoder.NumChannels, myFilterDecoder.Type))

	myResampler := foxResampler.NewResampler()
	myResampler.FromSampleRate = myFilterDecoder.SampleRate
	myResampler.ToSampleRate = targetSampleRate
	myResampler.Quality = 60
	myResampler.DebugOn = true
	myResampler.DebugFunc = myLogger.Debug
	var WG sync.WaitGroup
	DecodedSamplesChannel := make(chan [][]float64, 1)

	WG.Add(1)
	go func() {
		defer func() {
			close(DecodedSamplesChannel) // Close the channel after decoding
			WG.Done()
		}()
		err := myFilterDecoder.DecodeSamples(DecodedSamplesChannel, nil)
		if err != nil {
			myLogger.Error(MsgHeader + "Decoder failed: " + err.Error())
			return
		} else {
			//myLogger.Debug(MsgHeader + fmt.Sprintf(" number of samples decoded %v", myFilterDecoder.TotalSamples))
		}
	}()

	WG.Add(1)
	go func() {
		defer WG.Done()
		outputSamples := make([][]float64, myFilterDecoder.NumChannels)
		for i := range outputSamples {
			outputSamples[i] = make([]float64, 0)
		}
		//myLogger.Debug(MsgHeader + " Structure built now build output Samples...")
		for samples := range DecodedSamplesChannel {
			for channelIdx, channelData := range samples {
				outputSamples[channelIdx] = append(outputSamples[channelIdx], channelData...)
			}
		}
		//myLogger.Debug(MsgHeader + " Ready to Normalize...")
		impulseSamples = outputSamples

	}()

	WG.Wait()

	myResampler.InputSamples = impulseSamples
	err = myResampler.Resample()
	if err != nil {
		myLogger.Error(MsgHeader + "Resampling failed: " + err.Error())
		return nil, err
	}

	return myResampler.InputSamples, nil
} // <-- LoadImpulse ends here

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
func CombineFilters(filterImpulse [][]float64, myPEQ foxPEQ.PEQFilter, NumChannels int, targetSampleRate int, myLogger *foxLog.Logger) ([]foxConvolver.Convolver, error) {
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
			myLogger.Debug(packageName + ": Merging PEQ and FIR Filters")
			myConvolvers = MergePEQandFIRFilters(&myPEQ, filterImpulse, myLogger)
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
	var maxPeak float64 = 0.0
	for i := range myConvolvers {
		myPeak := CalibrateImpulse(myConvolvers[i].FilterImpulse, float64(targetSampleRate))
		if myPeak > maxPeak {
			maxPeak = myPeak
		}

	}
	targetLevel := 1.0
	myLogger.Debug(packageName + "Convolver Filters " + fmt.Sprintf("Calibrated Peak: %v", maxPeak))

	for i := range myConvolvers {
		myConvolvers[i].FilterImpulse = foxNormalizer.NormalizeAudioChannel(myConvolvers[i].FilterImpulse, targetLevel, maxPeak)
	}

	myLogger.Debug(packageName + "Convolver Filters " + fmt.Sprintf("Number of channels %v, length of impulse %v", len(myConvolvers), len(myConvolvers[0].FilterImpulse)))
	return myConvolvers, nil

}

func MergePEQandFIRFilters(myPEQ *foxPEQ.PEQFilter,
	impulseSamples [][]float64, myLogger *foxLog.Logger) []foxConvolver.Convolver {
	// At this point we have a single channel PEQ impulse and an n channel FIR impulse
	//let put in a loop vs myPEQ Impulse against impulseSamples
	myConvolvers := make([]foxConvolver.Convolver, len(impulseSamples))

	myLogger.Debug("Convolve FIR and PEQ Filters")
	//
	allImpulses := make([][]float64, len(myConvolvers))

	for i := range impulseSamples {
		// cpoy the PEQ filter so that it is now the convolver filter
		myConvolvers[i].FilterImpulse = make([]float64, len(myPEQ.Impulse))
		copy(myConvolvers[i].FilterImpulse, myPEQ.Impulse)
		// and convolve it with the N normalized impulse
		allImpulses[i] = myConvolvers[i].ConvolveFFT(impulseSamples[i])
		//some cleanup needed here
		//myLogger.Debug("FFT Convolver Filters " + fmt.Sprintf("length of impulse %v for convolver %v", len(allImpulses[i]), i))
	}
	// We want to normalize the combined impulse Unfortunately it is one impulse per convolver, maybe design is wrong!

	//myLogger.Debug("Normalize Combined FIR and PEQ Filters " + fmt.Sprintf("Number of channels %v, length of impulse %v", len(allImpulses), len(allImpulses[0])))
	if len(allImpulses) > 0 && len(allImpulses) == len(myConvolvers) {
		// 4. Copy normalized impulses back
		for i, impulse := range allImpulses {
			if len(impulse) == 0 {
				myLogger.Error("Zero length impulse")
			}
			//myLogger.Debug("Pre-Copy Convolver Filters " + fmt.Sprintf("length of impulse %v for convolver %v", len(impulse), i))

			myConvolvers[i].FilterImpulse = impulse
			//myLogger.Debug("Post-Copy Convolver Filters " + fmt.Sprintf("length of impulse %v for convolver %v", len(myConvolvers[i].FilterImpulse), i))
		}
	} else {
		myLogger.Error("Convolver " + fmt.Sprintf("Mismatch between Number of convolvers %v and number of impulses %v", len(myConvolvers), len(allImpulses)))

		return nil
	}

	myLogger.Debug("Convolver " + fmt.Sprintf("Number of convolvers %v", len(myConvolvers)) + " Convolver Filters " + fmt.Sprintf(" length of impulse %v", len(myConvolvers[0].FilterImpulse)))
	return myConvolvers
}

// dBFS to Linear RMS
func dBFSToLinear(dBFS float64) float64 {
	return math.Pow(10, dBFS/20)
}

func GetWidthCoefficients(widthDB float64) (float64, float64) {
	if widthDB == 0 {
		return 1.0, 1.0 // Neutral gains for no width adjustment
	}

	midGainDB := -widthDB / 2
	sideGainDB := widthDB / 2

	midGainLinear := dBFSToLinear(midGainDB)
	sideGainLinear := dBFSToLinear(sideGainDB)

	sumSquares := midGainLinear*midGainLinear + sideGainLinear*sideGainLinear
	k := math.Sqrt(2 / sumSquares)

	return midGainLinear * k, sideGainLinear * k
}

func GetChannelsGain(myConfig *LyrionDSPSettings.ClientConfig, numChannels int, myLogger *foxLog.Logger) []float64 {

	gains := make([]float64, numChannels)
	// Convert preamp dB to linear
	preampLinear := dBFSToLinear(myConfig.Preamp)
	// Initialize gains with preamp

	for i := range numChannels {
		gains[i] = preampLinear
	}

	// Apply balance SAFELY (only attenuate, never boost) and only to first two channels
	if myConfig.Balance > 0 {
		// Positive balance = favor right → attenuate left
		gains[0] *= dBFSToLinear(-myConfig.Balance) // Attenuate left
	} else if myConfig.Balance < 0 {
		// Negative balance = favor left → attenuate right
		gains[1] *= dBFSToLinear(myConfig.Balance) // balanceDB is negative
	}

	// Clamp gains to prevent invalid values (optional)
	for i := range numChannels {
		gains[i] = math.Max(0, math.Min(gains[i], 1.0))
	}
	return gains
}

// generateSineSweepWithSilence, generates a 1 second sine wave wotha a max peak of 1.0 for calibration of impulse
func generateSineSweepWithSilence(sampleRate, durationSec, peak float64) []float64 {
	numSamples := int(sampleRate * durationSec)
	sweep := make([]float64, numSamples)
	startFreq := 20.0  // 20 Hz
	endFreq := 22000.0 // 22 kHz
	t := 0.0

	for i := range numSamples {
		freq := startFreq * math.Pow(endFreq/startFreq, t/durationSec)
		sweep[i] = peak * math.Sin(2*math.Pi*freq*t)
		t += 1.0 / sampleRate
	}

	// Add 10 ms of silence at start/end
	padSamples := int(0.01 * sampleRate) // 10 ms
	paddedSweep := make([]float64, len(sweep)+2*padSamples)
	copy(paddedSweep[padSamples:padSamples+len(sweep)], sweep)
	return paddedSweep
}

func generateWhiteNoise(sampleRate, durationSec float64, peak float64) []float64 {
	numSamples := int(sampleRate * durationSec)
	noise := make([]float64, numSamples)

	for i := 0; i < numSamples; i++ {
		noise[i] = (rand.Float64()*2 - 1) * peak
	}
	return noise
}

// This function runs a 1 second sweep through the impulse and returns the peak value
func CalibrateImpulse(impulse []float64, sampleRate float64) float64 {
	var output []float64
	fastCalibration := true
	if fastCalibration {

		sweep := generateWhiteNoise(sampleRate, 0.1, 1.0)
		// in this scenario the sweep is likely shorter than the impulse so invert it
		myConvolver := foxConvolver.NewConvolver(sweep)
		output = myConvolver.ConvolveFFT(impulse)
	} else {
		sweep := generateSineSweepWithSilence(sampleRate, 1.0, 1.0)

		myConvolver := foxConvolver.NewConvolver(impulse)
		output = myConvolver.ConvolveFFT(sweep)
	}
	// Trim pre/post silence (10 ms) from output
	padSamples := int(0.01 * sampleRate) // 10 ms
	validOutput := output[padSamples : len(output)-padSamples]

	return foxNormalizer.CalculateMaxGain(validOutput)

}

// Delay structure for managing delay processing
type Delay struct {
	sampleRate float64     // Shared sample rate (Hz)
	Delays     []int       // Delay in samples per channel
	Buffers    [][]float64 // Delay buffers for each channel
}

// NewDelay creates a delay system for `channelCount` channels with zero delay.
func NewDelay(channelCount int, sampleRate float64) *Delay {
	return &Delay{
		sampleRate: sampleRate,
		Delays:     make([]int, channelCount),
		Buffers:    make([][]float64, channelCount),
	}
}

// AddDelay configures a delay for a specific channel (in milliseconds).
func (d *Delay) AddDelay(channel int, delayMs float64) {
	if channel < 0 || channel >= len(d.Delays) {
		return // Invalid channel
	}

	// Convert ms to samples (rounded)
	delaySamples := int(math.Round((delayMs * d.sampleRate) / 1000.0))
	if delaySamples < 0 {
		delaySamples = 0
	}

	d.Delays[channel] = delaySamples

	// Initialize buffer with zeros to enforce initial delay
	if delaySamples > 0 {
		d.Buffers[channel] = make([]float64, delaySamples) // Pre-filled with zeros
	} else {
		d.Buffers[channel] = nil
	}
}
