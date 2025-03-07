package LyrionDSPSettings

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// AppSettings holds the configuration settings from the JSON file.
type AppSettings struct {
	Settings struct {
		Partitions    string `json:"partitions"`
		ConvolverGain string `json:"convolvergain"`
		SoxExe        string `json:"soxExe"`
		Tail          string `json:"tail"`
		Dither        string `json:"dither"`
		Debug         string `json:"debug"`
		LogFile       string `json:"logFile"`
		Gain          string `json:"gain"`
	} `json:"settings"`
}

// LoadAppSettings reads a JSON file from the specified folder and path and unmarshals it into AppSettings.
func LoadAppSettings(filePath string) (*AppSettings, error) {

	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	byteValue, _ := io.ReadAll(file)

	var settings AppSettings
	json.Unmarshal(byteValue, &settings)
	return &settings, nil
}

type Arguments struct {
	UserID          string
	IsRawIn         bool
	IsRawOut        bool
	BigEndian       bool
	InPath          string
	OutPath         string
	OutBits         int
	InputSampleRate int
	RawBits         int
	RawChan         int
	skipTime        string        // Change to string for input and local only
	StartTime       time.Duration // Change to time.Duration for output
	Debug           bool
}

func ReadArgs() (*Arguments, error) {
	args := &Arguments{}

	// Define command-line flags
	flag.StringVar(&args.UserID, "id", "", "User ID")
	flag.BoolVar(&args.IsRawIn, "wav", false, "Input has a WAV header")
	flag.BoolVar(&args.IsRawOut, "wavo", false, "Output should have a WAV header")
	flag.BoolVar(&args.BigEndian, "be", false, "Input is big-endian")
	flag.StringVar(&args.InPath, "input", "", "Input file path")
	flag.StringVar(&args.OutPath, "output", "", "Output file path")
	flag.IntVar(&args.OutBits, "d", 0, "Output bits")
	flag.IntVar(&args.InputSampleRate, "r", 0, "Input sample rate")
	flag.IntVar(&args.RawBits, "b", 0, "Raw bits")
	flag.IntVar(&args.RawChan, "c", 0, "Raw channels")
	flag.StringVar(&args.skipTime, "skip", "", "Start time in min:sec.fraction format") // Updated to SkipTime

	flag.BoolVar(&args.Debug, "debug", false, "Enable debug mode")

	// Parse the flags
	flag.Parse()

	// Convert SkipTime to StartTime
	if err := convertSkipTimeToDuration(args); err != nil {
		return nil, err
	}

	return args, nil
}

func convertSkipTimeToDuration(args *Arguments) error {
	ts := args.skipTime
	if ts == "" {
		return nil
	}
	mySeparator := []string{":", "."}
	tsa := strings.Split(ts, mySeparator[0])

	if len(tsa) == 2 {
		seconds := tsa[1]
		min, err := strconv.Atoi(tsa[0])
		if err != nil {
			return err
		}

		secParts := strings.Split(seconds, mySeparator[1])
		sec, err := strconv.Atoi(secParts[0])
		if err != nil {
			return err
		}

		var fraction float64
		if len(secParts) > 1 {
			fraction, err = strconv.ParseFloat("0."+secParts[1], 64)
			if err != nil {
				return err
			}
		}

		totalSeconds := float64(min*60+sec) + fraction
		args.StartTime = time.Duration(totalSeconds * float64(time.Second)) // Updated to set StartTime
	} else {
		// Handle other cases or fallback logic if needed
		return fmt.Errorf("invalid time format: %s", ts)
	}

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
