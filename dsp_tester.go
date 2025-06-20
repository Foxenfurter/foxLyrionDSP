package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	colorReset = "\033[0m"
	colorGreen = "\033[32m"
	colorRed   = "\033[31m"
	colorCyan  = "\033[36m"
)

type TestCase struct {
	Name    string `yaml:"name"`
	Command string `yaml:"command"`
	Check1  string `yaml:"check1"`
	Check2  string `yaml:"check2"`
}

type TestSuite struct {
	Tests []TestCase `yaml:"tests"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Printf("%sUsage: %s <test-file.yaml>%s\n", colorRed, os.Args[0], colorReset)
		return
	}

	testFile := os.Args[1]
	testSuite, err := loadTestSuite(testFile)
	if err != nil {
		fmt.Printf("%sError loading test suite: %v%s\n", colorRed, err, colorReset)
		return
	}

	currentDir, err := os.Getwd()
	if err != nil {
		fmt.Printf("%sError getting current directory: %v%s\n", colorRed, err, colorReset)
		return
	}

	fmt.Printf("%sRunning tests from: %s%s\n", colorCyan, currentDir, colorReset)
	fmt.Printf("%sLoaded %d test cases from: %s%s\n", colorCyan, len(testSuite.Tests), testFile, colorReset)

	for _, tc := range testSuite.Tests {
		runTest(tc, currentDir)
	}
}

func loadTestSuite(filename string) (*TestSuite, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	var testSuite TestSuite
	if err := yaml.Unmarshal(data, &testSuite); err != nil {
		return nil, fmt.Errorf("parsing YAML: %w", err)
	}

	return &testSuite, nil
}

func runTest(tc TestCase, currentDir string) {
	fmt.Printf("\n%s=== Running test: %s ===%s\n", colorCyan, tc.Name, colorReset)
	
	// Create unique batch file name
	batchFile := filepath.Join(currentDir, tc.Name+".bat")
	defer os.Remove(batchFile) // Clean up after test
	
	// Create batch file content with proper working directory
	batchContent := fmt.Sprintf(
		`@echo off
cd /D "%s"
%s
exit %%ERRORLEVEL%%`,
		currentDir, tc.Command)

	// Write batch file
	if err := os.WriteFile(batchFile, []byte(batchContent), 0644); err != nil {
		fmt.Printf("%sError creating batch file: %v%s\n", colorRed, err, colorReset)
		return
	}

	// Create output directory if needed
	outputDir := filepath.Join(currentDir, "AudioOutput")
	if err := os.MkdirAll(outputDir, os.ModePerm); err != nil {
		fmt.Printf("%sError creating output directory: %v%s\n", colorRed, err, colorReset)
	}

	// Execute batch file
	cmd := exec.Command("cmd", "/C", batchFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf("%sCommand: %s%s\n", colorCyan, tc.Command, colorReset)

	startTime := time.Now()
	err := cmd.Run()
	duration := time.Since(startTime)

	if err != nil {
		fmt.Printf("%sTest failed after %v: %v%s\n", 
			colorRed, duration.Round(time.Millisecond), err, colorReset)
	} else {
		fmt.Printf("%sTest completed in %v%s\n", 
			colorGreen, duration.Round(time.Millisecond), colorReset)
	}

	// Check log file
	logFile := filepath.Join(currentDir, "logs", "squeezedsp.log")
	lastLine, err := getLastNonEmptyLine(logFile)
	if err != nil {
		fmt.Printf("%sError reading log file: %v%s\n", colorRed, err, colorReset)
		fmt.Printf("%s - SampleCheck: %sFAIL%s\n", tc.Name, colorRed, colorReset)
		fmt.Printf("%s - PeakCheck: %sFAIL%s\n", tc.Name, colorRed, colorReset)
	} else {
		fmt.Printf("%sLast log entry: %s%s\n", colorCyan, lastLine, colorReset)
		checkAndReport(tc.Name, "SampleCheck", lastLine, tc.Check1)
		checkAndReport(tc.Name, "PeakCheck", lastLine, tc.Check2)
	}
}

func getLastNonEmptyLine(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	// Read the entire file (log files are typically small)
	content, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}

	// Split into lines
	lines := strings.Split(string(content), "\n")
	
	// Find last non-empty line
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return line, nil
		}
	}
	
	return "", fmt.Errorf("no non-empty lines found in log file")
}

func checkAndReport(testName, checkName, logLine, expected string) {
	if strings.Contains(logLine, expected) {
		fmt.Printf("%s - %s: %sPASS%s\n", 
			testName, checkName, colorGreen, colorReset)
	} else {
		fmt.Printf("%s - %s: %sFAIL%s\n", 
			testName, checkName, colorRed, colorReset)
		fmt.Printf("\tExpected to find: %s\n", expected)
		fmt.Printf("\tIn actual line : %s\n", logLine)
	}
}