package LyrionDSPSettings

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Foxenfurter/foxAudioLib/foxLog"
)

func InitializeSettings() (*Arguments, *AppSettings, *ClientConfig, *foxLog.Logger, error) {
	// Read command-line arguments
	myArgs, err := ReadArgs()
	if err != nil {
		displayUsage(err)
		return nil, nil, nil, nil, err
	}

	// Load application settings
	AppSettingFile := myArgs.AppName + ".json"
	myAppSettings, err := LoadAppSettings(AppSettingFile)
	if err != nil {
		return myArgs, nil, nil, nil, fmt.Errorf("settings load error: %w\n", err)
	}

	// Determine debug state (command line takes precedence)
	DebugEnabled := myArgs.Debug
	if !DebugEnabled {
		if strings.ToLower(myAppSettings.Debug) == "true" {
			DebugEnabled = true
		}
	}

	// Initialize logger
	logFilePath := filepath.Clean(myAppSettings.LogFile)                        // Sanitize path
	logger, err := foxLog.NewLogger(logFilePath, myArgs.ClientID, DebugEnabled) // Changed ClientID to UserID
	if err != nil {
		return myArgs, myAppSettings, nil, nil, fmt.Errorf("logger init error: %w", err)
	}

	// Load configuration
	myClientFilePrefix := sanitizeClientID(myArgs.ClientID)
	myConfigFile := myAppSettings.SettingsDataFolder + "/" + myClientFilePrefix + ".json"
	config, err := LoadConfig(myConfigFile)
	if err != nil {
		return myArgs, myAppSettings, nil, logger, fmt.Errorf("config load error: %w", err)
	}

	return myArgs, myAppSettings, config, logger, nil
}

func displayUsage(err error) {
	if err != nil {
		fmt.Printf("Error with Command Line: %s\n", err)
	}
	// change this to align with arguments

	fmt.Println("Expect command arguments:  --id=clientID [--d=outputbitdepth] [--r=samplerate] [--wav] [--be]")

}

func sanitizeClientID(clientID string) string {
	// Replace ':' with '_'
	clientID = strings.ReplaceAll(clientID, ":", "_")

	// Replace '-' with '_'
	clientID = strings.ReplaceAll(clientID, "-", "_")

	return clientID
}

// AppSettings holds the configuration settings from the JSON file.

type AppSettings struct {
	Partitions         string `json:"partitions"`
	ConvolverGain      string `json:"convolvergain"`
	SoxExe             string `json:"soxExe"`
	Tail               string `json:"tail"`
	Dither             string `json:"dither"`
	Debug              string `json:"debug"`
	LogFile            string `json:"logFile"`
	Gain               string `json:"gain"`
	PluginDataFolder   string `json:"pluginDataFolder"`
	SettingsDataFolder string `json:"settingsDataFolder"`
	ImpulseDataFolder  string `json:"impulseDataFolder"`
	TempDataFolder     string `json:"tempDataFolder"`
}

// appConfigWrapper is used to parse the nested JSON structure
type appConfigWrapper struct {
	Settings AppSettings `json:"settings"`
}

// LoadAppSettings reads the JSON file and unmarshals it into AppSettings
func LoadAppSettings(filePath string) (*AppSettings, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	byteValue, _ := io.ReadAll(file)

	var wrapper appConfigWrapper
	err = json.Unmarshal(byteValue, &wrapper)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %v", err)
	}

	return &wrapper.Settings, nil
}

// Arguments holds the command-line arguments for the application.
type Arguments struct {
	AppName  string
	ClientID string
	//IsRawIn         bool
	InputFormat     string
	OutputFormat    string
	BigEndian       bool
	InPath          string
	OutPath         string
	OutBits         int
	InputSampleRate int
	PCMBits         int
	PCMChannels     int
	skipTime        string        // Change to string for input and local only
	StartTime       time.Duration // Change to time.Duration for output
	Debug           bool
}

// ReadArgs reads the command-line arguments and returns an Arguments object.
// Argument labels are case-insensitive.
func ReadArgs() (*Arguments, error) {
	args := &Arguments{}

	// Get executable name
	if len(os.Args) > 0 {
		args.AppName = strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	}

	// Parse arguments manually with case-insensitive flag handling
	if err := parseArgs(args); err != nil {
		return nil, err
	}

	// Validate required arguments - these need tidying up
	if args.ClientID == "" {
		return nil, fmt.Errorf("missing required argument: --ClientID")
	}

	// if we have a raw input, we need to validate the raw bits and channels and sample rate
	if args.InputFormat == "PCM" {
		if args.InputSampleRate == 0 {
			return nil, fmt.Errorf("missing sample rate for PCM file: --r")
		}
		if args.PCMBits == 0 {
			return nil, fmt.Errorf("missing raw bits for PCM file: --b")
		}
		if args.PCMChannels == 0 {
			return nil, fmt.Errorf("missing raw channels for PCM file: --c")
		}
	}

	// Validate numeric ranges
	if args.OutBits != 0 && (args.OutBits < 8 || args.OutBits > 32) {
		return nil, fmt.Errorf("invalid output bits: %d (must be 8-32)", args.OutBits)
	}

	// Convert and validate skip time
	if err := convertSkipTimeToDuration(args); err != nil {
		return nil, fmt.Errorf("invalid --skip format: %w", err)
	}

	return args, nil
}

func parseArgs(args *Arguments) error {
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if strings.HasPrefix(arg, "--") {
			flagName := strings.ToLower(arg[2:]) // Remove "--" and lowercase
			value := ""

			// Check if the flag has a value (e.g., --id=value)
			if strings.Contains(flagName, "=") {
				parts := strings.SplitN(flagName, "=", 2)
				flagName = parts[0]
				value = parts[1]
			}

			switch flagName {
			case "clientid":
				args.ClientID = value
			case "formatin":
				args.InputFormat = value
			case "formatout":
				args.OutputFormat = value
				//big endian
			case "be":
				args.BigEndian = true // Assume no value means true
			case "input":
				args.InPath = value
			case "output":
				args.OutPath = value
				//bit depth output
			case "bitsout":
				val, err := strconv.Atoi(value)
				if err != nil {
					return fmt.Errorf("invalid value for -d: %w", err)
				}
				args.OutBits = val
			case "samplerate":
				val, err := strconv.Atoi(value)
				if err != nil {
					return fmt.Errorf("invalid value for -r: %w", err)
				}
				args.InputSampleRate = val
				//bit depths
			case "bitsin":
				val, err := strconv.Atoi(value)
				if err != nil {
					return fmt.Errorf("invalid value for -b: %w", err)
				}
				args.PCMBits = val
				//audio channels
			case "channels":
				val, err := strconv.Atoi(value)
				if err != nil {
					return fmt.Errorf("invalid value for -c: %w", err)
				}
				args.PCMChannels = val
			case "skip":
				args.skipTime = value
			case "debug":
				args.Debug = true
			default:
				return fmt.Errorf("unknown flag: %s", arg)
			}
		}
	}
	return nil
}

func convertSkipTimeToDuration(args *Arguments) error {
	if args.skipTime == "" {
		return nil // No skip time provided
	}

	parts := strings.Split(args.skipTime, ":")
	if len(parts) != 2 && len(parts) != 3 {
		return fmt.Errorf("invalid format")
	}

	minutes, err := strconv.Atoi(parts[0])
	if err != nil {
		return fmt.Errorf("invalid minutes: %w", err)
	}

	seconds, err := strconv.Atoi(parts[1])
	if err != nil {
		return fmt.Errorf("invalid seconds: %w", err)
	}

	duration := time.Duration(minutes)*time.Minute + time.Duration(seconds)*time.Second

	if len(parts) == 3 {
		fraction, err := strconv.ParseFloat(parts[2], 64)
		if err != nil {
			return fmt.Errorf("invalid fraction: %w", err)
		}
		duration += time.Duration(fraction * float64(time.Second))
	}

	args.StartTime = duration
	return nil
}

// Config
type BiquadFilter struct {
	FilterType string
	Enabled    bool
	Frequency  float64
	Gain       float64
	SlopeType  string  // "Q" in this case
	Slope      float64 // Q value
}

type Delay struct {
	Units string
	Value float64
}

type Loudness struct {
	Enabled        bool
	ListeningLevel float64
}

type ClientConfig struct {
	Filters    []BiquadFilter
	Preamp     float64
	Name       string
	Bypass     bool
	Preset     string
	FIRWavFile string
	Version    string
	Width      float64
	Balance    float64
	Delay      Delay
	Loudness   Loudness
	// Add other fields as needed
}

type rawClientConfig struct {
	Client map[string]json.RawMessage `json:"Client"`
}

// LoadConfig loads the configuration from a specified JSON file and returns a ClientConfig.
func LoadConfig(filePath string) (*ClientConfig, error) {
	// Open the JSON file
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("error opening file: %w", err)
	}
	defer file.Close()

	// Read the file contents
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("error reading file: %w", err)
	}

	// Pass the data to buildConfig to create a new ClientConfig
	config, err := buildConfig(data)
	if err != nil {
		return nil, fmt.Errorf("error building config: %w", err)
	}

	return config, nil
}

func buildConfig(data []byte) (*ClientConfig, error) {
	var raw rawClientConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	config := &ClientConfig{Filters: make([]BiquadFilter, 0)}

	for key, value := range raw.Client {
		switch {
		case strings.HasPrefix(key, "EQBand_"):
			var pf struct {
				Gain json.Number `json:"gain"`
				Freq json.Number `json:"freq"`
				Q    json.Number `json:"q"`
			}
			if err := json.Unmarshal(value, &pf); err == nil {
				if isNullFilter(pf.Gain, pf.Freq, pf.Q) {
					continue // Skip null filters
				}
				config.Filters = append(config.Filters, createPeakingFilter(pf))
			}

		case key == "Lowshelf":
			var ls struct {
				Gain    json.Number `json:"gain"`
				Freq    json.Number `json:"freq"`
				Slope   json.Number `json:"slope"`
				Enabled json.Number `json:"enabled"`
			}
			if err := json.Unmarshal(value, &ls); err == nil {
				config.Filters = append(config.Filters, createShelfFilter("LowShelf", ls))
			}

		case key == "Highshelf":
			var hs struct {
				Gain    json.Number `json:"gain"`
				Freq    json.Number `json:"freq"`
				Slope   json.Number `json:"slope"`
				Enabled json.Number `json:"enabled"`
			}
			if err := json.Unmarshal(value, &hs); err == nil {
				config.Filters = append(config.Filters, createShelfFilter("HighShelf", hs))
			}

		case key == "Lowpass", key == "Highpass":
			var lp struct {
				Freq    json.Number `json:"freq"`
				Q       json.Number `json:"q"`
				Enabled json.Number `json:"enabled"`
			}
			if err := json.Unmarshal(value, &lp); err == nil {
				filterType := "LowPass"
				if key == "Highpass" {
					filterType = "HighPass"
				}
				config.Filters = append(config.Filters, createPassFilter(filterType, lp))
			}

		case key == "Preamp":
			if s, err := parseRaw2Number(value); err == nil {
				config.Preamp, err = s.Float64()
				if err != nil {
					//output error message
					return nil, err
				}
			}

		case key == "Name":
			if err := json.Unmarshal(value, &config.Name); err == nil {
				config.Name = strings.Trim(config.Name, "\"")
			}
			// Add other fields as needed
		case key == "Bypass":
			if err := json.Unmarshal(value, &config.Name); err == nil {
				config.Name = strings.Trim(config.Name, "\"")
			}
		case key == "Preset":
			if err := json.Unmarshal(value, &config.Name); err == nil {
				config.Name = strings.Trim(config.Name, "\"")
			}
		case key == "FIRWavFile":
			if err := json.Unmarshal(value, &config.FIRWavFile); err == nil {
				config.FIRWavFile = strings.Trim(config.FIRWavFile, "\"")
			}
		case key == "Width":
			if s, err := parseRaw2Number(value); err == nil {
				config.Width, err = s.Float64()
				if err != nil {
					//output error message
					return nil, err
				}
			}
		case key == "Balance":
			if s, err := parseRaw2Number(value); err == nil {
				config.Balance, err = s.Float64()
				if err != nil {
					//output error message
					return nil, err
				}
			}
		case key == "Loudness":
			var ls struct {
				Enabled json.Number `json:"enabled"`
				Level   json.Number `json:"listening_level"`
			}
			if err := json.Unmarshal(value, &ls); err == nil {
				config.Loudness.Enabled = parseBool(ls.Enabled)

				config.Loudness.ListeningLevel = parseNumber(ls.Level)
			}
		case key == "Delay":
			var ls struct {
				Delay json.Number `json:"delay"`
				Units string      `json:"units"`
			}
			if err := json.Unmarshal(value, &config.Delay); err == nil {
				config.Delay.Value = parseNumber(ls.Delay)

				config.Delay.Units = ls.Units
			}
		}

	}
	return config, nil
}

func createPeakingFilter(pf struct {
	Gain json.Number `json:"gain"`
	Freq json.Number `json:"freq"`
	Q    json.Number `json:"q"`
}) BiquadFilter {
	return BiquadFilter{
		FilterType: "Peaking",
		Enabled:    true, // Assume enabled if present
		Frequency:  parseNumber(pf.Freq),
		Gain:       parseNumber(pf.Gain),
		SlopeType:  "Q",
		Slope:      parseNumber(pf.Q),
	}
}

func createShelfFilter(filterType string, sf struct {
	Gain    json.Number `json:"gain"`
	Freq    json.Number `json:"freq"`
	Slope   json.Number `json:"slope"`
	Enabled json.Number `json:"enabled"`
}) BiquadFilter {
	return BiquadFilter{
		FilterType: filterType,
		Enabled:    parseBool(sf.Enabled),
		Frequency:  parseNumber(sf.Freq),
		Gain:       parseNumber(sf.Gain),
		SlopeType:  "Q",
		Slope:      parseNumber(sf.Slope),
	}
}

func createPassFilter(filterType string, pf struct {
	Freq    json.Number `json:"freq"`
	Q       json.Number `json:"q"`
	Enabled json.Number `json:"enabled"`
}) BiquadFilter {
	return BiquadFilter{
		FilterType: filterType,
		Enabled:    parseBool(pf.Enabled),
		Frequency:  parseNumber(pf.Freq),
		SlopeType:  "Q",
		Slope:      parseNumber(pf.Q),
	}
}

func parseRaw2Number(rawMsg json.RawMessage) (json.Number, error) {
	var num json.Number
	err := json.Unmarshal(rawMsg, &num)
	if err != nil {
		num = "0"
		return num, err
	}
	return num, nil
}

func parseNumber(n json.Number) float64 {
	if s := n.String(); s != "" && s != "null" {
		f, _ := strconv.ParseFloat(s, 64)
		return f
	}
	return 0
}

func parseBool(n json.Number) bool {
	s := n.String()
	return s == "1" || strings.ToLower(s) == "true"
}

func isNullFilter(values ...json.Number) bool {
	for _, v := range values {
		if v.String() != "null" {
			return false
		}
	}
	return true
}
