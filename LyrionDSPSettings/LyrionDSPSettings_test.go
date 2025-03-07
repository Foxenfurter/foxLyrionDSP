package LyrionDSPSettings

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestLoadConfig tests the LoadConfig function
func TestLoadConfig(t *testing.T) {
	configFilePath := "C:\\ProgramData\\Squeezebox\\Prefs\\SqueezeDSP\\Settings\\d8_43_ae_4d_e8_a6.settings.json" // Update this with the correct path to your JSON config file
	configFilePath = filepath.Clean(configFilePath)

	config, err := LoadConfig(configFilePath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Print the loaded config for confirmation
	fmt.Printf("Loaded Client Config: %+v\n\n", config)
}

// TestLoadAppSettings tests the LoadAppSettings function
func TestLoadAppSettings(t *testing.T) {
	appSettingsFilePath := "C:\\ProgramData\\Squeezebox\\Cache\\InstalledPlugins\\Plugins\\SqueezeDSP\\Bin\\SqueezeDSP_config.json" // Update this with the correct path to your app settings JSON file
	appSettingsFilePath = filepath.Clean(appSettingsFilePath)
	appSettings, err := LoadAppSettings(appSettingsFilePath) // Assuming LoadAppSettings is defined
	if err != nil {
		t.Fatalf("Failed to load app settings: %v", err)
	}

	// Print the loaded app settings for confirmation
	fmt.Printf("Loaded App Settings: %+v\n\n", appSettings)
}

func TestReadArgs(t *testing.T) {
	// Save the original os.Args
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }() // Restore original args after the test

	// Set up test arguments
	testArgs := []string{"SqueezeDSP", "--wav=false"}
	os.Args = testArgs

	// Call the ReadArgs function
	myArgs, err := ReadArgs()
	if err != nil {
		t.Fatalf("ReadArgs() error = %v", err)
	}
	// Print the loaded arguments for confirmation
	fmt.Printf("Loaded Arguments: %+v\n\n", myArgs)
}
