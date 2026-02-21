package LyrionDSPFilters

import (
	"fmt"
	"math"
	"math/rand"
	"sync"

	foxConvolver "github.com/Foxenfurter/foxAudioLib/foxConvolverPartition" // CHANGED: Use partitioned convolver
	"github.com/Foxenfurter/foxAudioLib/foxLog"
	"github.com/Foxenfurter/foxAudioLib/foxNormalizer"
	"github.com/Foxenfurter/foxAudioLib/foxPEQ"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPSettings"
)

const packageName = "LyrionDSPFilters"
const signalDivisor = 3 // when we calculate the signal block length we divide the sample rate by this number

// CleanUpImpulse removes any leading or trailing silence from the impulse
func NewPEQFilter(targetSampleRate int, myLogger *foxLog.Logger) *foxPEQ.PEQFilter {
	myPEQ := foxPEQ.NewPEQFilter(targetSampleRate, 15) // Create a single PEQFilter
	myPEQ.DebugFunc = myLogger.Debug
	return &myPEQ
}

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
			myLogger.Debug(packageName + ": Invalid filter definition, " + err.Error())
			return nil, err
		}
		generateImpulse = true
	}
	// Apply filters if enabled and there are filters in config
	numberOfFilters := len(myConfig.Filters)
	if len(myConfig.Filters) > 0 {
		for _, filter := range myConfig.Filters {
			err := myPEQ.CalcBiquadFilter(filter.FilterType, filter.Frequency, filter.Gain, filter.Slope, filter.SlopeType)
			// log error and continue - we will still generate an impulse
			if err != nil {
				myLogger.Debug(packageName + ": Filter Ignored: " + err.Error())
				numberOfFilters--
			} else {
				generateImpulse = true
			}
		}

	}
	if generateImpulse {
		myLogger.Debug(packageName + ": PEQ Filter Built - using " + fmt.Sprintf(" %v", numberOfFilters) + " filters")
		myPEQ.UpdateFilterLength()
		//myPEQ.GenerateFilterImpulse()

	}

	return &myPEQ, nil
}

func NormalizeImpulse(myImpulse [][]float64, targetLevel float64, myLogger *foxLog.Logger) ([][]float64, error) {
	const functionName = "NormalizeImpulse"
	const MsgHeader = packageName + ": " + functionName + ": "
	myLogger.Debug(MsgHeader + "Normalizing impulse...")
	maxGain := 0.0

	for i := range myImpulse {
		maxGain = math.Max(maxGain, foxConvolver.MaxGainFromFFT(myImpulse[i]))

	}
	myLogger.Debug(MsgHeader + "Normalizing using gain: " + fmt.Sprintf("%.4f", maxGain))
	for i := range myImpulse {
		myImpulse[i] = foxNormalizer.NormalizeAudioChannel(myImpulse[i], targetLevel, maxGain)
	}

	return myImpulse, nil
}

// CombineFilters - Combine the filter impulse with the PEQ impulse and handle appropriate scenarios where one, both or neither are present
// CHANGED: Now returns []*foxConvolver.PartitionedConvolver instead of []foxConvolver.Convolver
// CombineFilters prepares the impulse data, normalizes it, and builds the Convolvers once.
func CombineFilters(filterImpulse [][]float64, myPEQ *foxPEQ.PEQFilter, NumChannels int, targetSampleRate int, myLogger *foxLog.Logger) ([]*foxConvolver.PartitionedConvolver, error) {

	// 1. DATA PREPARATION
	// We will build the final audio buffers first, before creating convolver objects.
	var finalImpulses [][]float64
	err := error(nil)
	hasFIR := len(filterImpulse) > 0
	hasPEQ := len(myPEQ.FilterCoefficients) > 0
	//NeedNormalization := false
	// Handle Channel Mapping (Mono FIR to Stereo)

	if hasFIR && len(filterImpulse) == 1 && NumChannels == 2 {
		original := filterImpulse[0]
		copyData := make([]float64, len(original))
		copy(copyData, original)
		filterImpulse = append(filterImpulse, copyData)
	}

	// Handle Channel Trimming
	if hasFIR && len(filterImpulse) > NumChannels {
		myLogger.Debug(packageName + fmt.Sprintf(": Trimming impulse from %d to %d channels", len(filterImpulse), NumChannels))
		filterImpulse = filterImpulse[:NumChannels]
	}
	switch {
	// Logic Branch: Determine the base impulse data
	case hasFIR && hasPEQ:
		myLogger.Debug(packageName + ": Merging PEQ and FIR Filters")
		finalImpulses = ApplyPEQToFIR(filterImpulse, myPEQ, targetSampleRate, myLogger)
		//NeedNormalization = true
		myLogger.Debug(packageName + ": Merging PEQ and FIR Filters - Done")

	case hasFIR && hasPEQ && 1 == 2:
		myLogger.Debug(packageName + ": Merging PEQ and FIR Filters")
		// Helper now returns raw float data
		//finalImpulses = MergePEQandFIRFilters(myPEQ, filterImpulse, targetSampleRate, myLogger) // CHANGED: Get raw data back from merge function

		//NeedNormalization = true // Merging can change gain, so we will normalize after mergingNeedNormalization
		myLogger.Debug(packageName + ": Merging PEQ and FIR Filters - Done")
	case hasFIR:
		myLogger.Debug(packageName + ": No PEQ Filter - using FIR")
		finalImpulses = filterImpulse
		//NeedNormalization = true
	case hasPEQ:

		myLogger.Debug(packageName + ": No FIR Filter - using PEQ")
		// Ensure the PEQ impulse has been generated (or generate it now)
		if len(myPEQ.Impulse) == 0 {
			myPEQ.GenerateFilterImpulse() // This should use the current FilterLength
		}
		finalImpulses = make([][]float64, NumChannels)
		for i := 0; i < NumChannels; i++ {
			imp := make([]float64, myPEQ.FilterLength) // Allocate based on PEQ's filter length
			copy(imp, myPEQ.Impulse)
			finalImpulses[i] = imp
		}
		myLogger.Debug(packageName + ": No FIR or PEQ Filter")
		finalImpulses = make([][]float64, NumChannels)
		for i := range finalImpulses {
			finalImpulses[i] = make([]float64, 0)
		}
		//NeedNormalization = true
	}
	// 2. NORMALIZATION
	// Normalize the raw data BEFORE creating the Convolver objects.
	// This ensures the FFT partitions are calculated using the correct gain.
	/*if NeedNormalization {
		targetLevel := 0.94406 // Approx -0.5 dBFS

		myLogger.Debug(packageName + ": Normalising impulse to target level " + fmt.Sprintf("%.4f", targetLevel))
		if len(finalImpulses[0]) > 0 {
			finalImpulses, _ = NormalizeImpulse(finalImpulses, targetLevel, myLogger) // Normalize to target level without pre-scaling

		}
	}
	*/
	//trim the final impulses to the correct length based on the PEQ filter length (if PEQ is present) or the FIR length (if no PEQ)
	finalImpulses, err = CleanUpImpulse(finalImpulses, targetSampleRate, -80.0, myLogger)
	if err != nil {
		myLogger.Debug(packageName + "Error cleaning final impulse: " + err.Error())
	}

	// 3. OBJECT CREATION
	// Create convolvers once with the final, correct data.
	myConvolvers := make([]*foxConvolver.PartitionedConvolver, NumChannels)

	var wg sync.WaitGroup
	for i := range myConvolvers {
		wg.Add(1)
		go func(channel int) {
			defer wg.Done()
			// This constructor calculates FFT partitions immediately using the passed data
			myConvolvers[channel] = foxConvolver.NewPartitionedConvolver(finalImpulses[channel], targetSampleRate)

		}(i)
	}
	wg.Wait()
	// just for testing

	if len(myConvolvers) > 0 {
		myLogger.Debug(packageName + "Convolver Filters " + fmt.Sprintf("Number of channels %v, length of impulse %v", len(myConvolvers), len(myConvolvers[0].FilterImpulse)))
	}

	return myConvolvers, nil
}

// using peq.FilterLength to allocate enough extra samples for the tail.
func ApplyPEQToFIR(firImpulse [][]float64, peq *foxPEQ.PEQFilter, sampleRate int, logger *foxLog.Logger) [][]float64 {
	if len(firImpulse) == 0 || peq == nil || len(peq.FilterCoefficients) == 0 {
		return firImpulse
	}

	// Total combined length ≈ FIR length + PEQ impulse length (decay fully captured)
	// We add peq.FilterLength as extra samples; the filter will ring into the zero padding.
	merged := make([][]float64, len(firImpulse))

	for ch := range firImpulse {
		fir := firImpulse[ch]
		// Allocate: FIR + extra samples (safe upper bound)
		buf := make([]float64, len(fir)+peq.FilterLength)
		copy(buf, fir) // rest is zero

		// Apply each biquad section in cascade
		current := buf
		for _, coeff := range peq.FilterCoefficients {
			output := make([]float64, len(current))
			foxPEQ.IIRFilter(current, coeff.B, coeff.A, output)
			current = output
		}
		merged[ch] = current
	}

	// Optional: Trim using your existing CleanUpImpulse (which works on [][]float64)
	// trimmed, _ := CleanUpImpulse(merged, sampleRate, -80.0, logger)
	// return trimmed
	return merged
}

// MergePEQandFIRFilters takes the PEQ impulse and the FIR impulse(s) and merges them together using convolution, returning the raw merged impulse data.
func MergePEQandFIRFilters(myPEQ []float64, impulseSamples [][]float64, targetSampleRate int, myLogger *foxLog.Logger) [][]float64 {

	mergedImpulses := make([][]float64, len(impulseSamples))
	var wg sync.WaitGroup

	for i := range impulseSamples {
		wg.Add(1)
		go func(channel int) {
			defer wg.Done()

			// Create a temporary convolver just for the math
			// We use the PEQ as the "Filter" and the FIR file as the "Input Signal" (or vice versa, convolution is commutative)
			tempConvolver := foxConvolver.NewPartitionedConvolver(myPEQ, targetSampleRate)

			// ConvolveFFT returns the resulting float slice
			mergedImpulses[channel] = tempConvolver.ConvolveFFT(impulseSamples[channel])
		}(i)
	}
	wg.Wait()

	return mergedImpulses
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

// generateSineSweepWithSilence, generates a 1 second sine wave with a max peak of 1.0 for calibration of impulse
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

// CalibrateImpulse runs a 1 second sweep through the impulse and returns the peak value
// CHANGED: Uses partitioned convolver
func CalibrateImpulse(impulse []float64, sampleRate float64) float64 {
	var output []float64
	fastCalibration := false
	if fastCalibration {
		sweep := generateWhiteNoise(sampleRate, 0.1, 1.0)
		// in this scenario the sweep is likely shorter than the impulse so invert it

		myConvolver := foxConvolver.NewPartitionedConvolver(sweep, int(sampleRate)) // CHANGED: Use constructor
		output = myConvolver.ConvolveFFT(impulse)
	} else {
		sweep := generateSineSweepWithSilence(sampleRate, 0.25, 1.0)
		myConvolver := foxConvolver.NewPartitionedConvolver(sweep, int(sampleRate)) // CHANGED: Use constructor

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
