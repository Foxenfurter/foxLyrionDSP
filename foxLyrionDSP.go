package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Foxenfurter/foxAudioLib/foxAudioEncoder"
	"github.com/Foxenfurter/foxAudioLib/foxBufferedStdinReader"
	"github.com/Foxenfurter/foxAudioLib/foxLog"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPFilters"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPProcessAudio"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPSettings"
)

// Main function
//
//	Initialize settings
//	Initialize Audio  input and output Headers
//	Build DSP Filters
//	Process audio
//	Cleanup
var (
	terminated     bool
	terminateMutex sync.Mutex
)

func main() {
	// Initialize settings, using LyrionDSPSettings - using a startup log to capture startup issues
	fatalError := false
	/*logFile, err := os.OpenFile("C:\\ProgramData\\Squeezebox\\Logs\\startup.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {

		panic(err)
	}
	*/
	// Early messages are queued up as the log is not ready yet
	ErrorMsg := "=== Program Initialisation Started ===\n"

	start := time.Now()
	useStdIn := false
	if foxBufferedStdinReader.IsStdinPipe() {
		useStdIn = true
	}

	myArgs, myAppSettings, myConfig, myLogger, err := LyrionDSPSettings.InitializeSettings()
	if err != nil {
		ErrorMsg += fmt.Sprintf("Error Initialising Settings: %v\n", err)
		fatalError = true

	}
	if myArgs.InPath != "" && useStdIn {
		ErrorMsg += fmt.Sprintf("Error: Receiving input from file and stdin, they are mutually exclusive\n")
		fatalError = true
	}
	if myArgs.InPath == "" && !useStdIn {
		ErrorMsg += fmt.Sprintf("Error: No input specified\n")
		fatalError = true
	}

	if fatalError {
		myLogger.FatalError(ErrorMsg)
		myLogger.Close()
		os.Exit(1)
	}
	end := time.Now()
	elapsed := end.Sub(start).Seconds()
	ErrorMsg += fmt.Sprintf("Settings Initialised in %.3f seconds\n", elapsed)

	//====================================
	// Try and catch early terminiation
	sigChan := make(chan os.Signal, 1)

	// Notify this channel for specific signals that indicate termination.
	// SIGINT is usually triggered by Ctrl+C.
	// SIGTERM is the standard termination signal sent by `kill` command.
	signal.Notify(sigChan,
		os.Interrupt,
		syscall.SIGTERM,
		syscall.SIGQUIT,
		syscall.SIGILL,
		syscall.SIGABRT,
		syscall.SIGFPE,
		syscall.SIGSEGV,
		syscall.SIGBUS,
		syscall.SIGPIPE,
		syscall.SIGHUP,
		//syscall.SIGUSR1, // Uncomment if you expect these
		//syscall.SIGUSR2, // Uncomment if you expect these
	)

	// Start a goroutine to listen for termination signals.
	go func() {
		sig := <-sigChan
		myLogger.FatalError("Received termination signal: " + sig.String())

		terminateMutex.Lock()
		terminated = true
		terminateMutex.Unlock()
		myLogger.FatalError("Signal detected, will exit when current operation finishes...")
		// You might want to perform immediate cleanup here if necessary.
	}()

	//====================================

	myLogger.Info("=== LyrionDSP - Started: " + myConfig.Name + " Settings Initialised in: " + fmt.Sprintf("%.3f", elapsed) + " seconds ===")

	// Check if SqueezeDSP is in bypass mode
	myLogger.Debug("Bypass mode set to: " + fmt.Sprintf("%v", myConfig.Bypass))
	if myConfig.Bypass {
		bypassProcess(myLogger)
	}

	myLogger.Debug("Command Line Arguments: " + fmt.Sprintf("%v", myArgs))
	// Initialize Audio Headers
	// Initialize processor

	myDecoder, myEncoder, err := LyrionDSPProcessAudio.InitializeAudioHeaders(myArgs, myAppSettings, myConfig, myLogger)
	if err != nil {
		myLogger.FatalError("Error Initialising Audio Headers: " + err.Error())
		os.Exit(1)
	}
	end = time.Now()
	elapsed = end.Sub(start).Seconds()
	//24/44100 PCM => 24/44100 PCM TRIANGULAR, preamp -5 db, internal gain -19 dB
	// Build DSP Filters

	targetSampleRate := myDecoder.SampleRate
	//used for normalization - may need to add this as a configurable item in the future
	targetLevel := 0.75

	//newFileName := fmt.Sprintf("%s_%d.wav", fileName, myConvSampleRate)

	var myImpulse [][]float64
	saveImpulse := false
	myTempFirFilter := ""

	baseFileName := strings.TrimSuffix(filepath.Base(myConfig.FIRWavFile), filepath.Ext(myConfig.FIRWavFile))
	// no impulse
	//myLogger.Debug("Trying to load impulse: " + baseFileName)
	if baseFileName == "." {
		myLogger.Debug("No impulse specified")
		myImpulse = [][]float64{[]float64{}}
	} else {

		baseFileName = baseFileName + "_" + fmt.Sprintf("%d", targetSampleRate) + ".wav"
		myTempFirFilter = filepath.Join(myAppSettings.TempDataFolder, baseFileName)

		//inputFile string, outputFile string, targetSampleRate int, targetBitDepth int, myLogger *foxLog.Logger
		// try and load resampled file first
		myLogger.Debug("Trying resampled Impulse First : " + myTempFirFilter)
		myImpulse, err = LyrionDSPFilters.LoadImpulse(myTempFirFilter, targetSampleRate, targetLevel, myLogger)
		if err != nil {
			// if that fails then try with original file
			if strings.Contains(err.Error(), "does not exist") {
				// File not found case
				myLogger.Debug("Resampled Impulse does not exist, trying original: " + myConfig.FIRWavFile)
				myImpulse, err = LyrionDSPFilters.LoadImpulse(myConfig.FIRWavFile, targetSampleRate, targetLevel, myLogger)
				if err != nil {
					myLogger.Error("Error loading impulse: " + err.Error())
				} else {
					saveImpulse = true
				}
			} else {
				myLogger.Error("Error loading impulse: " + err.Error()) // Other errors
			}
		}
	}

	myLogger.Debug("Create PEQ Filter")
	myPEQ, err := LyrionDSPFilters.BuildPEQFilter(myConfig, myAppSettings, targetSampleRate, myLogger)
	if err != nil {
		myLogger.FatalError("Error building PEQ: " + err.Error())

	}
	myLogger.Debug("Combine Filters")
	myConvolvers, err := LyrionDSPFilters.CombineFilters(myImpulse, *myPEQ, myDecoder.NumChannels, targetSampleRate, myLogger)
	if err != nil {
		myLogger.FatalError("Error combining filters: " + err.Error())
	}

	myLogger.Info(fmt.Sprintf(" %v/%v %s BigEndian %v => %v/%v %s Noise Shaped Initialised in %.3f seconds",
		myDecoder.BitDepth, myDecoder.SampleRate, myDecoder.Type, myDecoder.BigEndian, myEncoder.BitDepth, myEncoder.SampleRate, myEncoder.Type, elapsed))

	if saveImpulse {
		// Backup Impulses
		targetBitDepth := 16
		myLogger.Debug("Backup Impulse: " + myTempFirFilter)
		go foxAudioEncoder.WriteWavFile(
			myTempFirFilter,
			myImpulse,
			targetSampleRate,
			targetBitDepth,
			len(myImpulse),
			false, // i.e. do not overwrite
			myLogger,
		)
	} else {
		myLogger.Debug("No impulse to backup")
	}

	end = time.Now()
	initTime := end.Sub(start).Seconds()
	myLogger.Info(fmt.Sprintf("DSP Filters Built in %.3f seconds", initTime))

	// Process audio
	LyrionDSPProcessAudio.ProcessAudio(&myDecoder, &myEncoder, myLogger, myConfig, myConvolvers, myAppSettings, myArgs)

	peakDBFS := LyrionDSPProcessAudio.PeakDBFS(myEncoder.Peak)
	end = time.Now()
	elapsed = end.Sub(start).Seconds()
	//11423050 samples, 241703.3034 ms (659.6813 init), 1.0717 * realtime, peak -8.6029 dBfs
	expectedSeconds := float64(myEncoder.NumSamples) / float64(myEncoder.SampleRate)
	relativeSpeed := expectedSeconds / elapsed

	/*	myLogger.Info(fmt.Sprintf(" %v samples, ", myEncoder.NumSamples) +
			fmt.Sprintf(" %.3f seconds", elapsed) +
			fmt.Sprintf(" (%.3f init), ", initTime) +
			fmt.Sprintf(" %v inputrate, ", myDecoder.SampleRate) +
			fmt.Sprintf(" %v outputrate, ", myEncoder.SampleRate) +
			fmt.Sprintf(" %f preamp, ", myConfig.Preamp) +
			fmt.Sprintf(" %v channels, ", myEncoder.NumChannels) +
			fmt.Sprintf(" peak %f dBfs\n", peakDBFS))
		myLogger.Debug(fmt.Sprintf(" %.3f relative speed\n", relativeSpeed))
	*/
	// Go code to match C# log format
	myLogger.Info(fmt.Sprintf(
		"%d samples, %.3f ms (%.3f init), %.4f * realtime, peak %.4f dBfs\n",
		myEncoder.NumSamples, // n (samples)
		elapsed*1000,         // Convert seconds to milliseconds (e.g., 103.810 ms)
		initTime*1000,        // Convert init time to milliseconds
		relativeSpeed,        // realtime/runtime (e.g., 1.255)
		peakDBFS,             // dBfs peak value
	))
	// Close the output file after all processing is done
	err = myEncoder.Close()
	if err != nil {
		myLogger.Error("Error closing Encoder: " + err.Error())
		// You might want to handle this error more explicitly
	}
	myLogger.Close()
}

func bypassProcess(myLogger *foxLog.Logger) {
	myLogger.Info("Bypass mode enabled")
	n, err := io.Copy(os.Stdout, os.Stdin)
	if err != nil {
		myLogger.FatalError("Pipeline error: " + err.Error() + "\n" + fmt.Sprintf("Transferred %d bytes\n", n))
		os.Exit(1)
	}
	myLogger.Info("Completed successfully. Transferred " + fmt.Sprintf("%d bytes", n))
	os.Exit(1)
}

/*

	myLogger.Info("Bypass mode enabled")
	buffer := make([]byte, 8192)
	totalBytes := 0
	totalRead := 0

	for {
		nRead, errRead := os.Stdin.Read(buffer)
		totalRead += nRead

		if nRead > 0 {
			bytesWritten := 0
			for bytesWritten < nRead {
				nWrite, errWrite := os.Stdout.Write(buffer[bytesWritten:nRead])
				if errWrite != nil {
					myLogger.FatalError("Write error: " + errWrite.Error())
					os.Exit(1)
				}
				bytesWritten += nWrite
				totalBytes += nWrite
			}
		}

		if errRead != nil {
			if errRead == io.EOF {
				break
			}
			myLogger.FatalError("Read error: " + errRead.Error())
			os.Exit(1)
		}
	}

	// Final flush
	if err := os.Stdout.Sync(); err != nil {
		myLogger.FatalError("Sync error: " + err.Error())
		myLogger.Close()
		os.Exit(1)
	}

	myLogger.Info(fmt.Sprintf("Completed. Read: %d, Wrote: %d", totalRead, totalBytes))
	myLogger.Close()
	os.Exit(0)

*/
