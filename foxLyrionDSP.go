package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"

	"sync"
	"syscall"
	"time"

	"github.com/Foxenfurter/foxAudioLib/foxBufferedStdinReader"
	"github.com/Foxenfurter/foxAudioLib/foxLog"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPProcessAudio"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPSettings"
)

// Main function
//
//	Initialize settings
//	Initialize Audio  input and output Headers
//	Initialise Process (Build DSP filters, delay etc)
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
	InFormat := myArgs.InputFormat
	if InFormat == "" {
		InFormat = "WAV"
	}
	OutFormat := myArgs.OutputFormat
	if OutFormat == "" {
		OutFormat = "WAV"
	}
	if myConfig.Bypass {
		// Simple Bypass mode - just copy input to output
		if InFormat == OutFormat {
			bypassProcess(myLogger)
		}
	}

	myLogger.Debug("Command Line Arguments: " + fmt.Sprintf("%v", myArgs))
	// Initialize Audio Headers
	// Initialize processor
	myProcessor := &LyrionDSPProcessAudio.AudioProcessor{}
	myProcessor.Config = myConfig
	myProcessor.Logger = myLogger
	myProcessor.AppSettings = myAppSettings
	myProcessor.Args = myArgs
	err = myProcessor.Initialize()

	//myDecoder, myEncoder, err := LyrionDSPProcessAudio.InitializeAudioHeaders(myArgs, myAppSettings, myConfig, myLogger)
	if err != nil {
		myLogger.FatalError("Error Initialising Audio Headers: " + err.Error())
		os.Exit(1)
	}
	end = time.Now()
	elapsed = end.Sub(start).Seconds()
	myLogger.Info(fmt.Sprintf(" %v/%v %s => %v/%v %s , preamp: %v db, internal gain %v db,Initialised in %.3f seconds",
		myProcessor.Decoder.BitDepth,
		myProcessor.Decoder.SampleRate,
		myProcessor.Decoder.Type,
		//Endianess,
		myProcessor.Encoder.BitDepth,
		myProcessor.Encoder.SampleRate,
		myProcessor.Encoder.Type,
		myConfig.Preamp,
		0.0,
		elapsed))

	end = time.Now()
	initTime := end.Sub(start).Seconds()
	//myLogger.Info(fmt.Sprintf("DSP Filters Built in %.3f seconds", initTime))

	// Process audio
	if !myConfig.Bypass {
		myProcessor.ProcessAudio()
	} else {
		myProcessor.ByPassProcess()
	}
	peakDBFS := LyrionDSPProcessAudio.PeakDBFS(myProcessor.Encoder.Peak)
	end = time.Now()
	elapsed = end.Sub(start).Seconds()
	//11423050 samples, 241703.3034 ms (659.6813 init), 1.0717 * realtime, peak -8.6029 dBfs
	expectedSeconds := float64(myProcessor.Encoder.NumSamples) / float64(myProcessor.Encoder.SampleRate)
	relativeSpeed := expectedSeconds / elapsed

	rawPeakDBFS := LyrionDSPProcessAudio.PeakDBFS(myProcessor.Decoder.RawPeak)
	myLogger.Debug(fmt.Sprintf("rawPeak %f Input Peak %f OutputPeak %f Diff %f", myProcessor.Decoder.RawPeak, rawPeakDBFS, peakDBFS, peakDBFS-rawPeakDBFS))

	// Go code to match C# log format
	myLogger.Info(fmt.Sprintf(
		"%d samples, %.3f ms (%.3f init), %.4f * realtime, peak %.4f dBfs, input peak %.4f dBfs \n",
		myProcessor.Encoder.NumSamples, // n (samples)
		elapsed*1000,                   // Convert seconds to milliseconds (e.g., 103.810 ms)
		initTime*1000,                  // Convert init time to milliseconds
		relativeSpeed,                  // realtime/runtime (e.g., 1.255)
		peakDBFS,                       // dBfs peak value
		rawPeakDBFS,                    // Input peak value
	))
	// Close the output file after all processing is done
	err = myProcessor.Encoder.Close()
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
