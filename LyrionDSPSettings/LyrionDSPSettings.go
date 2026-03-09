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

	"github.com/Foxenfurter/foxAudioLib/foxFilters"
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
	AppSettingFile := myArgs.AppName + "_config.json"
	exeDir, err := getExecutableDir()
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("could not get executable directory: %w", err)
	}

	configPath := filepath.Join(exeDir, AppSettingFile)
	myAppSettings, err := LoadAppSettings(configPath)
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
	myClientFilePrefix := myArgs.CleanClientID
	myConfigFile := myAppSettings.SettingsDataFolder + "/" + myClientFilePrefix + ".settings.json"
	config, err := LoadConfig(myConfigFile)
	if err != nil {
		logger.Error(fmt.Sprintf("config load error (bypass mode): %v", err))
		config = &ClientConfig{Bypass: true}
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

func getExecutableDir() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("could not get executable path: %w", err)
	}
	return filepath.Dir(exePath), nil
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

	// Now lets set some defaults for the folders in case they have not been explicitly set
	if wrapper.Settings.PluginDataFolder == "" {
		return nil, fmt.Errorf("failed to find pluginDataFolder: %v", err)
	}
	if wrapper.Settings.SettingsDataFolder == "" {
		wrapper.Settings.SettingsDataFolder = filepath.Clean(wrapper.Settings.PluginDataFolder + "/Settings")
	}
	if wrapper.Settings.ImpulseDataFolder == "" {
		wrapper.Settings.ImpulseDataFolder = filepath.Clean(wrapper.Settings.PluginDataFolder + "/Impulses")
	}
	if wrapper.Settings.TempDataFolder == "" {
		wrapper.Settings.TempDataFolder = filepath.Clean(wrapper.Settings.PluginDataFolder + "/Temp")
	}

	return &wrapper.Settings, nil
}

// Arguments holds the command-line arguments for the application.
type Arguments struct {
	AppName       string
	ClientID      string
	CleanClientID string
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
	args.OutBits = 24
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
				// need the CleanClientID for use in file names
				args.CleanClientID = sanitizeClientID(value)
			case "formatin":
				args.InputFormat = value

			case "formatout":
				args.OutputFormat = value
				//big endian
			case "be":
				if value == "" || strings.ToLower(value) == "true" {
					args.BigEndian = true
				} else {
					args.BigEndian = false
				}
			case "input":
				args.InPath = value
			case "output":
				args.OutPath = value
				//bit depth output
			case "bitsout":
				val, err := strconv.Atoi(value)
				if err != nil {
					return fmt.Errorf("invalid value for --bitsout: %w", err)
				}
				args.OutBits = val
				if args.OutBits < 8 || args.OutBits > 32 {
					args.OutBits = 24
				}
			case "samplerate":
				val, err := strconv.Atoi(value)
				if err != nil {
					return fmt.Errorf("invalid value for --samplerate: %w", err)
				}
				args.InputSampleRate = val
				//bit depths
			case "bitsin":
				val, err := strconv.Atoi(value)
				if err != nil {
					return fmt.Errorf("invalid value for --bitsin: %w", err)
				}
				args.PCMBits = val
				//audio channels
			case "channels":
				val, err := strconv.Atoi(value)
				if err != nil {
					return fmt.Errorf("invalid value for --channels: %w", err)
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
type ClientConfig struct {
	foxFilters.PlayerConfig // embedded — all DSP fields accessible directly

	// Application identity fields only
	Name      string
	ClientID  string
	Bypass    bool
	Preset    string
	Version   string
	TimeStamp string
}

type rawClientConfig struct {
	Client map[string]json.RawMessage `json:"Client"`
}

// LoadConfig loads the configuration from a specified JSON file and returns a ClientConfig.
// fixed issue where filtesrs were built when they were disabled and peak filters built when values were zero.

func LoadConfig(filePath string) (*ClientConfig, error) {
	// Open the JSON file

	file, err := os.Open(filePath)

	if err != nil {
		return nil, fmt.Errorf("error opening file: %w", err)
	}
	defer file.Close()
	// Get file info to access modification time
	fileInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("error getting file info: %w", err)
	}
	modTime := fileInfo.ModTime()
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
	// Set TimeStamp to the file's last modification time in RFC3339 format
	config.TimeStamp = modTime.UTC().Format(time.RFC3339)

	return config, nil
}

func buildConfig(data []byte) (*ClientConfig, error) {
	var raw rawClientConfig // has the Client map[string]json.RawMessage field
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	config := &ClientConfig{}

	// ── Legacy format normalisation — Lyrion specific ─────────────────────
	// Translate legacy fields into current Filters array format BEFORE
	// calling the library. The library never sees the legacy structure.
	legacyFilter := translateLegacyFilters(raw.Client)

	// ── DSP fields — library call ─────────────────────────────────────────
	dspConfig, err := foxFilters.BuildPlayerConfig(legacyFilter)
	if err != nil {
		return nil, err
	}

	config.PlayerConfig = *dspConfig

	// ── Application fields ────────────────────────────────────────────────
	for key, value := range raw.Client {
		switch key {
		case "Name":
			json.Unmarshal(value, &config.Name)
			config.Name = strings.Trim(config.Name, "\"")
		case "ID":
			json.Unmarshal(value, &config.ClientID)
			config.ClientID = strings.Trim(config.ClientID, "\"")
		case "Bypass":
			var b json.Number
			if err := json.Unmarshal(value, &b); err == nil {
				config.Bypass = parseBool(b)
			}
		case "Preset":
			json.Unmarshal(value, &config.Preset)
			config.Preset = strings.Trim(config.Preset, "\"")
		case "Version":
			json.Unmarshal(value, &config.Version)
			config.Version = strings.Trim(config.Version, "\"")
		}
	}
	return config, nil
}

// translateLegacyFilters translates EQBand_, Lowshelf, Highshelf,
// Lowpass, Highpass into the current Filters array format so the
// library only ever sees one consistent structure.
func translateLegacyFilters(raw map[string]json.RawMessage) map[string]json.RawMessage {
	// Work on a copy so original is unchanged
	translated := make(map[string]json.RawMessage)
	for k, v := range raw {
		translated[k] = v
	}

	var filters []json.RawMessage

	// Absorb any existing Filters array entries first
	if existing, ok := translated["Filters"]; ok {
		json.Unmarshal(existing, &filters)
	}

	// Translate EQBand_ entries → Peak filter entries
	for key, value := range raw {
		if strings.HasPrefix(key, "EQBand_") {
			var pf struct {
				Gain  json.Number `json:"gain"`
				Freq  json.Number `json:"freq"`
				Slope json.Number `json:"q"`
			}
			if err := json.Unmarshal(value, &pf); err == nil {
				if !isNullFilter(pf.Gain, pf.Freq, pf.Slope) {
					entry, _ := json.Marshal(map[string]any{
						"FilterType": "Peak",
						"Frequency":  pf.Freq,
						"Gain":       pf.Gain,
						"Slope":      pf.Slope,
						"SlopeType":  "Q",
					})
					filters = append(filters, entry)
				}
			}
			delete(translated, key)
		}
	}

	// Translate Lowshelf, Highshelf → shelf filter entries
	for _, key := range []string{"Lowshelf", "Highshelf"} {
		if value, ok := raw[key]; ok {
			var sf struct {
				Gain    json.Number `json:"gain"`
				Freq    json.Number `json:"freq"`
				Slope   json.Number `json:"slope"`
				Enabled json.Number `json:"enabled"`
			}
			if err := json.Unmarshal(value, &sf); err == nil && parseBool(sf.Enabled) {
				filterType := "LowShelf"
				if key == "Highshelf" {
					filterType = "HighShelf"
				}
				entry, _ := json.Marshal(map[string]any{
					"FilterType": filterType,
					"Frequency":  sf.Freq,
					"Gain":       sf.Gain,
					"Slope":      sf.Slope,
					"SlopeType":  "Q",
				})
				filters = append(filters, entry)
			}
			delete(translated, key)
		}
	}

	// Translate Lowpass, Highpass → pass filter entries
	for _, key := range []string{"Lowpass", "Highpass"} {
		if value, ok := raw[key]; ok {
			var pf struct {
				Freq    json.Number `json:"freq"`
				Slope   json.Number `json:"q"`
				Enabled json.Number `json:"enabled"`
			}
			if err := json.Unmarshal(value, &pf); err == nil && parseBool(pf.Enabled) {
				filterType := "LowPass"
				if key == "Highpass" {
					filterType = "HighPass"
				}
				entry, _ := json.Marshal(map[string]any{
					"FilterType": filterType,
					"Frequency":  pf.Freq,
					"Slope":      pf.Slope,
					"SlopeType":  "Q",
				})
				filters = append(filters, entry)
			}
			delete(translated, key)
		}
	}

	// Write translated Filters array back
	if len(filters) > 0 {
		translated["Filters"], _ = json.Marshal(filters)
	}

	return translated
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

/*
func buildConfigold(data []byte) (*ClientConfig, error) {
	var raw rawClientConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	config := &ClientConfig{Filters: make([]BiquadFilter, 0)}
	tmpBiquadFilter := BiquadFilter{}
	if filtersRaw, ok := raw.Client["Filters"]; ok {
		var filters []struct {
			FilterType string      `json:"FilterType"`
			Frequency  json.Number `json:"Frequency"`
			Gain       json.Number `json:"Gain"`
			Slope      json.Number `json:"Slope"`
			SlopeType  string      `json:"SlopeType"`
		}

		if err := json.Unmarshal(filtersRaw, &filters); err == nil {
			for _, f := range filters {
				// Normalize filter type casing
				filterType := strings.ToLower(f.FilterType)
				switch filterType {
				case "peak":
					filterType = "Peak"
				case "lowshelf":
					filterType = "LowShelf"
				case "highshelf":
					filterType = "HighShelf"
				case "lowpass":
					filterType = "LowPass"
				case "highpass":
					filterType = "HighPass"
				}

				config.Filters = append(config.Filters, BiquadFilter{
					FilterType: filterType,
					Enabled:    true, // All filters in array are enabled
					Frequency:  parseNumber(f.Frequency),
					Gain:       parseNumber(f.Gain),
					SlopeType:  f.SlopeType,
					Slope:      parseNumber(f.Slope),
				})
			}
		}
	}

	for key, value := range raw.Client {
		switch {
		case key == "Filters":
			continue // Already processed

		case strings.HasPrefix(key, "EQBand_"):
			var pf struct {
				Gain  json.Number `json:"gain"`
				Freq  json.Number `json:"freq"`
				Slope json.Number `json:"q"`
			}
			if err := json.Unmarshal(value, &pf); err == nil {
				if isNullFilter(pf.Gain, pf.Freq, pf.Slope) {
					continue // Skip null filters
				}
				tmpBiquadFilter = createPeakingFilter("Peak", pf)
				if tmpBiquadFilter.Enabled {
					config.Filters = append(config.Filters, tmpBiquadFilter)
				}
			}

		case key == "Lowshelf":
			var ls struct {
				Gain    json.Number `json:"gain"`
				Freq    json.Number `json:"freq"`
				Slope   json.Number `json:"slope"`
				Enabled json.Number `json:"enabled"`
			}
			if err := json.Unmarshal(value, &ls); err == nil {
				tmpBiquadFilter = createShelfFilter("LowShelf", ls)
				if tmpBiquadFilter.Enabled {
					config.Filters = append(config.Filters, tmpBiquadFilter)
				}
			}

		case key == "Highshelf":
			var hs struct {
				Gain    json.Number `json:"gain"`
				Freq    json.Number `json:"freq"`
				Slope   json.Number `json:"slope"`
				Enabled json.Number `json:"enabled"`
			}
			if err := json.Unmarshal(value, &hs); err == nil {
				tmpBiquadFilter = createShelfFilter("HighShelf", hs)
				if tmpBiquadFilter.Enabled {
					config.Filters = append(config.Filters, tmpBiquadFilter)
				}
			}

		case key == "Lowpass", key == "Highpass":
			var lp struct {
				Freq    json.Number `json:"freq"`
				Slope   json.Number `json:"q"`
				Enabled json.Number `json:"enabled"`
			}
			if err := json.Unmarshal(value, &lp); err == nil {
				filterType := "LowPass"
				if key == "Highpass" {
					filterType = "HighPass"
				}
				tmpBiquadFilter = createPassFilter(filterType, lp)
				if tmpBiquadFilter.Enabled {
					config.Filters = append(config.Filters, tmpBiquadFilter)
				}
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
		case key == "ID":
			if err := json.Unmarshal(value, &config.ClientID); err == nil {
				config.ClientID = strings.Trim(config.ClientID, "\"")
			}

			// Add other fields as needed
		case key == "Bypass":
			var bypassValue json.Number
			if err := json.Unmarshal(value, &bypassValue); err == nil {
				config.Bypass = parseBool(bypassValue)
			}

		case key == "Preset":
			if err := json.Unmarshal(value, &config.Preset); err == nil {
				config.Preset = strings.Trim(config.Preset, "\"")
			}
		case key == "FIRWavFile":
			if err := json.Unmarshal(value, &config.FIRWavFile); err == nil {
				if config.FIRWavFile == "-" {
					config.FIRWavFile = ""
				}
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
				if config.Loudness.Enabled {
					config.Loudness.ListeningLevel = parseNumber(ls.Level)
				}
			}
		case key == "Delay":
			var delay struct {
				Delay json.Number `json:"delay"`
				Units string      `json:"units"`
			}
			if err := json.Unmarshal(value, &delay); err == nil {
				config.Delay.Value = parseNumber(delay.Delay)

				config.Delay.Units = "ms"
			}
		}

	}
	return config, nil
}

func createPeakingFilter(filterType string, pf struct {
	Gain  json.Number `json:"gain"`
	Freq  json.Number `json:"freq"`
	Slope json.Number `json:"q"`
}) BiquadFilter {
	return BiquadFilter{
		FilterType: filterType,
		Enabled:    isNotZeroValue(pf.Gain), // Assume enabled if present
		Frequency:  parseNumber(pf.Freq),
		Gain:       parseNumber(pf.Gain),
		SlopeType:  "Q",
		Slope:      parseNumber(pf.Slope),
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
		Enabled:    parseBool(sf.Enabled), // Assume enabled if present
		Frequency:  parseNumber(sf.Freq),
		Gain:       parseNumber(sf.Gain),
		SlopeType:  "Q",
		Slope:      parseNumber(sf.Slope),
	}
}

func createPassFilter(filterType string, pf struct {
	Freq    json.Number `json:"freq"`
	Slope   json.Number `json:"q"`
	Enabled json.Number `json:"enabled"`
}) BiquadFilter {
	return BiquadFilter{
		FilterType: filterType,
		Enabled:    parseBool(pf.Enabled), // Assume enabled if present
		Frequency:  parseNumber(pf.Freq),
		SlopeType:  "Q",
		Slope:      parseNumber(pf.Slope),
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

func isNotZeroValue(n json.Number) bool {
	if s := n.String(); s != "" && s != "null" {
		f, _ := strconv.ParseFloat(s, 64)
		if f == 0 {
			return false
		}
		return true
	}
	return true
}
*/
