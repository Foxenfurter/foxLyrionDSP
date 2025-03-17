package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Foxenfurter/foxAudioLib/foxBufferedStdinReader"
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
	logFile, err := os.OpenFile("C:\\ProgramData\\Squeezebox\\Logs\\startup.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		panic(err)
	}

	exitCode := 0
	defer func() {
		log.Printf("=== Program ended ===\n")
		os.Exit(exitCode)
	}() // Single exit point

	defer func() {
		if r := recover(); r != nil {
			log.Printf("LMS_ERROR: %v\n", r)
			os.Exit(1)
		}
	}()

	// 2. Temporary Logging with Immediate Flushing

	log.Println("=== Program started ===")
	defer func() {
		logFile.Sync()
		logFile.Close()
	}()

	// Log results
	if err != nil {
		log.Printf("Pipeline error: %v", err)
		exitCode = 1
	}

	start := time.Now()
	useStdIn := false
	if foxBufferedStdinReader.IsStdinPipe() {
		useStdIn = true
	}

	myArgs, myAppSettings, myConfig, myLogger, err := LyrionDSPSettings.InitializeSettings()
	if err != nil {
		log.Println("Error Initialising Settings: " + err.Error())
		os.Exit(1)
	}
	if myArgs.InPath != "" && useStdIn {
		log.Println("Error: Receiving input from file and stdin, they are mutually exclusive")
		os.Exit(1)
	}
	if myArgs.InPath == "" && !useStdIn {
		log.Println("Error: No input specified")
		os.Exit(1)
	}
	end := time.Now()
	elapsed := end.Sub(start).Seconds()
	defer myLogger.Close()
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
		// syscall.SIGUSR1, // Uncomment if you expect these
		// syscall.SIGUSR2, // Uncomment if you expect these
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

	myLogger.Info("=======================================================================================================")
	myLogger.Info("LyrionDSP - Started: " + myConfig.Name + " Settings Initialised in: " + fmt.Sprintf("%.3f", elapsed) + " seconds")
	//myLogger.Info("Input format: " + myArgs.InputFormat + " Gain: " + myAppSettings.Gain + " Name: " + myConfig.Name + "\n")

	// Check if SqueezeDSP is in bypass mode
	myLogger.Debug("Bypass mode set to: " + fmt.Sprintf("%v", myConfig.Bypass))
	if myConfig.Bypass {
		myLogger.Debug("Bypass mode enabled")
		n, err := io.Copy(os.Stdout, os.Stdin)
		if err != nil {
			myLogger.Error("Pipeline error: " + err.Error() + "\n" + fmt.Sprintf("Transferred %d bytes\n", n))
			exitCode = 1
		}
		myLogger.Info("Completed successfully. Transferred " + fmt.Sprintf("%d bytes", n))
		os.Exit(1)
	}

	// Initialize Audio Headers
	myDecoder, myEncoder, err := LyrionDSPProcessAudio.InitializeAudioHeaders(myArgs, myAppSettings, myConfig, myLogger)
	if err != nil {
		myLogger.FatalError("Error Initialising Audio Headers: " + err.Error())
		os.Exit(1)
	}
	end = time.Now()
	elapsed = end.Sub(start).Seconds()
	myLogger.Info("Audio Headers Initialised in " + fmt.Sprintf("%.3f", elapsed) + " seconds")
	// Build DSP Filters
	myConvolvers, err := LyrionDSPFilters.BuildDSPFilters(&myDecoder, &myEncoder, myLogger, myConfig)
	if err != nil {
		myLogger.Error("Error Building DSP Filters: " + err.Error())
		myLogger.Close()
		os.Exit(1)
	}
	end = time.Now()
	elapsed = end.Sub(start).Seconds()
	myLogger.Info("DSP Filters Built in " + fmt.Sprintf("%.3f", elapsed) + " seconds")
	// Process audio
	LyrionDSPProcessAudio.ProcessAudio(&myDecoder, &myEncoder, myLogger, myConfig, myConvolvers)

	end = time.Now()
	elapsed = end.Sub(start).Seconds()
	myLogger.Info("Finished - Audio Processed in " + fmt.Sprintf("%.3f", elapsed) + " seconds\n")

}
