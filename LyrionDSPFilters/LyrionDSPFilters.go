package LyrionDSPFilters

import (
	"fmt"
	"math"
	"math/rand"

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

	// Decode the audio file
	myFilterDecoder := new(foxAudioDecoder.AudioDecoder)
	impulseSamples, err := myFilterDecoder.LoadFiletoSampleBuffer(inputFile, "WAV", myLogger)
	if err != nil {
		return nil, fmt.Errorf("%s: decoder init failed: %v", functionName, err)
	}
	if myLogger.DebugEnabled {
		myLogger.Debug(MsgHeader + fmt.Sprintf("Impulse Decoder initialized: SampleRate=%d, Channels=%d, Type=%s", myFilterDecoder.SampleRate, myFilterDecoder.NumChannels, myFilterDecoder.Type))
		/*	myLogger.Debug(fmt.Sprintf("  AudioFormat (internal): %s", myFilterDecoder.WavDecoder.GetAudioFormatString())) // What your code thinks the format is
			myLogger.Debug(fmt.Sprintf("  BitDepth (internal): %d", myFilterDecoder.BitDepth))                             // What your code thinks the bit depth is
			myLogger.Debug(fmt.Sprintf("  NumChannels: %d", myFilterDecoder.NumChannels))
			myLogger.Debug(fmt.Sprintf("  SampleRate: %d", myFilterDecoder.SampleRate))
			myLogger.Debug(fmt.Sprintf("  BigEndian: %t", myFilterDecoder.BigEndian))
			myLogger.Debug(fmt.Sprintf("  Data Size (bytes, fd.Size): %d", myFilterDecoder.Size))                                        // Size of the audio data chunk
			myLogger.Debug(fmt.Sprintf("  Reader Cursor (start of data, fd.ReaderCursor): %d", myFilterDecoder.WavDecoder.ReaderCursor)) // Where reader will start
			//myLogger.Debug(fmt.Sprintf("  Total File Length (w.length): %d", myFilterDecoder.WavDecoder.Length))
		*/
	}

	myResampler := foxResampler.NewResampler()
	myResampler.FromSampleRate = myFilterDecoder.SampleRate
	myResampler.ToSampleRate = targetSampleRate
	myResampler.Quality = 60
	myResampler.DebugOn = false
	myResampler.DebugFunc = myLogger.Debug
	myResampler.InputSamples = impulseSamples

	err = myResampler.Resample()
	if err != nil {
		myLogger.Error(MsgHeader + "Resampling failed: " + err.Error())
		return nil, err
	}

	return myResampler.InputSamples, nil
} // <-- LoadImpulse ends here

// function should remove any leading or trailing silence from the impulse
func CleanUpImpulse(myImpulse [][]float64, sampleRate int, thresholdDB float64, myLogger *foxLog.Logger) ([][]float64, error) {
	const (
		windowTaper = 0.010 // 10ms window taper time (seconds)
		// Minimum required impulse length
		lengthBufferFactor = 1.2 // Return original if within 20% of min
		peakLocationRatio  = 0.1 // 10% threshold for peak location
	)
	minLength := int(float64(sampleRate) * 0.2)
	if len(myImpulse) == 0 || len(myImpulse[0]) == 0 {
		return nil, fmt.Errorf("empty impulse input")
	}

	originalLength := len(myImpulse[0])
	minThreshold := int(math.Ceil(float64(minLength) * lengthBufferFactor))

	if originalLength < minThreshold {
		myLogger.Debug(fmt.Sprintf("Original length %d under buffer threshold (%d), returning unchanged",
			originalLength, minThreshold))
		return myImpulse, nil
	}

	// --- Enhanced peak detection with position tracking ---
	peak := 0.0
	peakIndex := -1 // Track sample index where peak occurs
	for _, channel := range myImpulse {
		for i, sample := range channel {
			abs := math.Abs(sample)
			if abs > peak {
				peak = abs
				peakIndex = i
			}
		}
	}
	if peak == 0 {
		return nil, fmt.Errorf("impulse is completely silent")
	}

	threshold := peak * math.Pow(10, thresholdDB/20)
	myLogger.Debug(fmt.Sprintf("Peak: %.4f @ sample %d (%.2f%%), Threshold: %.6f",
		peak, peakIndex, 100*float64(peakIndex)/float64(originalLength), threshold))

	numSamples := originalLength
	start, end := -1, -1

	// Find first sample above threshold
	for i := 0; i < numSamples; i++ {
		for _, channel := range myImpulse {
			if math.Abs(channel[i]) >= threshold {
				start = i
				break
			}
		}
		if start != -1 {
			break
		}
	}

	// Find last sample above threshold
	for i := numSamples - 1; i >= 0; i-- {
		for _, channel := range myImpulse {
			if math.Abs(channel[i]) >= threshold {
				end = i
				break
			}
		}
		if end != -1 {
			break
		}
	}

	if start == -1 || end == -1 || start > end {
		return nil, fmt.Errorf("no non-silent section found")
	}

	// --- Conditionally disable start trimming/tapering ---
	skipStartTaper := false
	safetyMargin := int(float64(sampleRate) * 0.05)

	// Check if peak is in first 10% of original length
	if float64(peakIndex) < float64(originalLength)*peakLocationRatio {
		myLogger.Debug("Peak in first 10% - preserving start without trim/taper")
		start = 0 // Disable start trimming
		skipStartTaper = true
	} else {
		// Apply normal safety margins
		start = max(0, start-safetyMargin)
	}
	end = min(numSamples-1, end+safetyMargin*2) // Always apply end margin

	// --- Minimum length enforcement ---
	currentLength := end - start + 1
	if currentLength < minLength {
		needed := minLength - currentLength
		expandStart := needed / 2
		expandEnd := needed - expandStart

		// Adjust expansion based on available space and peak constraints
		availableBefore := start
		availableAfter := numSamples - 1 - end

		// If preserving start, prevent expansion before start
		if skipStartTaper {
			expandStart = 0 // Can't expand before start
			expandEnd = needed
		}

		// Normal expansion adjustments
		if expandStart > availableBefore {
			expandEnd += expandStart - availableBefore
			expandStart = availableBefore
		}
		if expandEnd > availableAfter {
			expandStart += expandEnd - availableAfter
			expandEnd = availableAfter
		}

		// Apply expansion
		start = max(0, start-expandStart)
		end = min(numSamples-1, end+expandEnd)

		// Final length check
		if (end - start + 1) < minLength {
			return nil, fmt.Errorf("cannot reach minimum length %d (max possible: %d)",
				minLength, end-start+1)
		}
		myLogger.Debug(fmt.Sprintf("Expanded region to %d samples (start: %d, end: %d)",
			end-start+1, start, end))
	}

	// Trim impulse
	trimmed := make([][]float64, len(myImpulse))
	for c := range trimmed {
		trimmed[c] = myImpulse[c][start : end+1]
	}

	// --- Conditional window application ---
	if skipStartTaper {
		// Apply right-side taper only
		taperSamples := int(float64(sampleRate) * windowTaper)
		if taperSamples > len(trimmed[0]) {
			taperSamples = len(trimmed[0])
		}
		if taperSamples > 0 {
			for c := range trimmed {
				// Taper only the END of the impulse
				for i := 0; i < taperSamples; i++ {
					idx := len(trimmed[c]) - 1 - i
					factor := float64(i) / float64(taperSamples)
					trimmed[c][idx] *= factor
				}
			}
			myLogger.Debug(fmt.Sprintf("Applied right-only taper (%d samples)", taperSamples))
		}
	} else {
		// Apply standard bidirectional taper
		applyWindow(trimmed, windowTaper, float64(sampleRate), myLogger)
	}

	return trimmed, nil
}

func applyWindow(impulse [][]float64, taperTime, sampleRate float64, logger *foxLog.Logger) {
	if len(impulse) == 0 || len(impulse[0]) == 0 {
		return
	}

	totalSamples := len(impulse[0])
	taperSamples := int(math.Ceil(sampleRate * taperTime))
	taperSamples = int(math.Min(float64(taperSamples), float64(totalSamples/2))) // Prevent over-tapering

	logger.Debug(fmt.Sprintf("Applying %d sample window taper", taperSamples))

	// Create window function (cosine taper)
	window := make([]float64, taperSamples)
	for i := range window {
		window[i] = 0.5 * (1 - math.Cos(math.Pi*float64(i)/float64(taperSamples-1)))
	}

	// Apply window to all channels
	for c := range impulse {
		// Fade in
		for i := 0; i < taperSamples; i++ {
			impulse[c][i] *= window[i]
		}

		// Fade out
		for i := 0; i < taperSamples; i++ {
			idx := totalSamples - taperSamples + i
			impulse[c][idx] *= window[taperSamples-1-i]
		}
	}
}

func BuildPEQFilter(
	myConfig *LyrionDSPSettings.ClientConfig,
	myAppSettings *LyrionDSPSettings.AppSettings, targetSampleRate int, myLogger *foxLog.Logger) (*foxPEQ.PEQFilter, error) {

	myPEQ := foxPEQ.NewPEQFilter(targetSampleRate, 15) // Create a single PEQFilter
	myPEQ.DebugFunc = myLogger.Debug
	generateImpulse := false
	if myConfig.Loudness.Enabled {
		myLogger.Debug(packageName + ": Loudness Filter Enabled")
		myLoudnessFilter := foxPEQ.NewLoudness()
		myLoudnessFilter.PlaybackPhon = myConfig.Loudness.ListeningLevel

		dfpl, err := myLoudnessFilter.DifferentialSPL(1.0)
		if err != nil {
			myLogger.Debug(packageName + ": DifferentialSPL error unable to proceed - " + err.Error())
			return nil, err
		}
		err = myPEQ.GenerateEQLoudnessFilter(dfpl)
		if err != nil {
			myLogger.Debug(packageName + ": Invalid filter definitio, " + err.Error())
			return nil, err
		}
		generateImpulse = true
	}
	// Apply filters if enabled and there are filters in config
	if len(myConfig.Filters) > 0 {
		for _, filter := range myConfig.Filters {
			err := myPEQ.CalcBiquadFilter(filter.FilterType, filter.Frequency, filter.Gain, filter.Slope, filter.SlopeType)
			// log error and continue - we will still generate an impulse
			if err != nil {
				myLogger.Debug(packageName + ": Filter Ignored: " + err.Error())
			} else {
				generateImpulse = true
			}
		}

	}
	if generateImpulse {
		myLogger.Debug(packageName + ": PEQ Filter Built")
		myPEQ.GenerateFilterImpulse()
	}

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

	// if we only have a single channel impulse and we have stereo audio assume impulse is used for both channles
	if len(filterImpulse) == 1 && NumChannels == 2 {
		//make a clone of the original channel
		original := filterImpulse[0]
		copyData := make([]float64, len(original))
		copy(copyData, original)
		//add the copy to the original
		filterImpulse = append(filterImpulse, copyData)
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

// Function to calculate width coefficients
func GetWidthCoefficients(widthDB float64) (float64, float64) {
	if widthDB == 0 {
		return 0.5, 0.5 // Correct neutral gains
	}

	midGainDB := -widthDB / 2
	sideGainDB := widthDB / 2

	midGainLinear := dBFSToLinear(midGainDB)
	sideGainLinear := dBFSToLinear(sideGainDB)

	sumSquares := midGainLinear*midGainLinear + sideGainLinear*sideGainLinear
	k := math.Sqrt(0.5 / sumSquares) // Correct normalization

	return midGainLinear * k, sideGainLinear * k
}

func GetWidthCoefficientsold(widthDB float64) (float64, float64) {
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
	fastCalibration := false
	if fastCalibration {

		sweep := generateWhiteNoise(sampleRate, 0.1, 1.0)
		// in this scenario the sweep is likely shorter than the impulse so invert it
		myConvolver := foxConvolver.NewConvolver(sweep)
		output = myConvolver.ConvolveFFT(impulse)
	} else {
		sweep := generateSineSweepWithSilence(sampleRate, 0.25, 1.0)

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
