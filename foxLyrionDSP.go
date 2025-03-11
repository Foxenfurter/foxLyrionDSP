package main

import (
	"fmt"
	"os"
	"time"

	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPFilters"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPProcessAudio"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPSettings"
)

// Main function
//	Initialize settings
//	Initialize Audio  input and output Headers
//	Build DSP Filters
//	Process audio
//	Cleanup

func main() {
	// Initialize settings, using LyrionDSPSettings
	start := time.Now()
	myArgs, myAppSettings, myConfig, myLogger, err := LyrionDSPSettings.InitializeSettings()
	if err != nil {
		fmt.Println("Error Initialising Settings: " + err.Error())
		os.Exit(1)
	}
	end := time.Now()
	elapsed := end.Sub(start).Seconds()
	myLogger.Info("Settings Initialised in " + fmt.Sprintf("%.3f", elapsed) + " seconds\n")
	myLogger.Info("Input format: " + myArgs.InputFormat + " Gain: " + myAppSettings.Gain + " Name: " + myConfig.Name + "\n")

	defer myLogger.Close()

	// Initialize Audio Headers
	myDecoder, myEncoder, err := LyrionDSPProcessAudio.InitializeAudioHeaders(myArgs, myAppSettings, myConfig, myLogger)
	if err != nil {
		myLogger.FatalError("Error Initialising Audio Headers: " + err.Error())
		os.Exit(1)
	}
	end = time.Now()
	elapsed = end.Sub(start).Seconds()
	myLogger.Info("Audio Headers Initialised in " + fmt.Sprintf("%.3f", elapsed) + " seconds\n")
	// Build DSP Filters
	myConvolvers, err := LyrionDSPFilters.BuildDSPFilters(&myDecoder, &myEncoder, myLogger, myConfig)
	if err != nil {
		myLogger.Error("Error Building DSP Filters: " + err.Error())
		myLogger.Close()
		os.Exit(1)
	}
	end = time.Now()
	elapsed = end.Sub(start).Seconds()
	myLogger.Info("DSP Filters Built in " + fmt.Sprintf("%.3f", elapsed) + " seconds\n")
	// Process audio
	LyrionDSPProcessAudio.ProcessAudio(&myDecoder, &myEncoder, myLogger, myConfig, myConvolvers)
	end = time.Now()
	elapsed = end.Sub(start).Seconds()
	myLogger.Info("Audio Processed in " + fmt.Sprintf("%.3f", elapsed) + " seconds\n")

}
