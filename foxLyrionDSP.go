package main

import (
	"fmt"
	"path/filepath"

	"github.com/Foxenfurter/foxAudioLib/foxLog"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPSettings"
)

func main() {

	myArgs, err := LyrionDSPSettings.ReadArgs()
	if err != nil {
		fmt.Println("Error reading arguments:", err)
		return
	}

	logFileDir := "C:/temp/gotesting/output.log"

	DebugEnabled := myArgs.Debug
	// Create a logger instance for testing
	logFilePath := filepath.Join(logFileDir, "logtest.txt")

	logger, err := foxLog.NewLogger(logFilePath, "", DebugEnabled)
	if err != nil {
		fmt.Println("Error creating logger:", err)
		return
	}

	defer logger.Close()
}
