package foxLyrionDSP

import (
	"fmt"

	"github.com/Foxenfurter/foxAudioLib/foxAudioDecoder"
	"github.com/Foxenfurter/foxAudioLib/foxAudioEncoder"
	"github.com/Foxenfurter/foxAudioLib/foxLog"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPSettings"

	"os"
)

// Main function
//	Initialize settings
//	Initialize Audio  input and output Headers
//	Build DSP Filters
//	Process audio
//	Cleanup

func main() {
	// Initialize settings, using LyrionDSPSettings
	myArgs, myAppSettings, myConfig, myLogger, err := LyrionDSPSettings.InitializeSettings()
	if err != nil {
		fmt.Println("Error Initialising Settings: ", err)
		os.Exit(1)
	}

	myLogger.Info("Input format: " + myArgs.InputFormat + " Gain: " + myAppSettings.Gain + " Name: " + myConfig.Name + "\n")
	defer myLogger.Close()

}

func InitializeAudioHeaders(myArgs *LyrionDSPSettings.Arguments, myAppSettings *LyrionDSPSettings.AppSettings, myConfig *LyrionDSPSettings.ClientConfig, myLogger *foxLog.Logger) (foxAudioDecoder.AudioDecoder, foxAudioEncoder.AudioEncoder, error) {
	myDecoder := foxAudioDecoder.AudioDecoder{

		Type: "WAV",
	}
	// If no input file is specified, use stdin by default
	if myArgs.InPath != "" {
		myDecoder.Filename = myArgs.InPath
	}
	if myArgs.InputFormat == "PCM" {
		myDecoder.Type = "PCM"
		myDecoder.SampleRate = myArgs.InputSampleRate
		myDecoder.BitDepth = myArgs.PCMBits
		myDecoder.NumChannels = myArgs.PCMChannels
		myDecoder.BigEndian = myArgs.BigEndian
	} else {
		myDecoder.Type = "WAV"
		err := myDecoder.Initialise()
		if err != nil {
			return nil, nil, err
		}
	}

	// now setup encoder
	// Initialize Audio Encoder
	myEncoder := foxAudioEncoder.AudioEncoder{
		Type:        "WAV",
		SampleRate:  myDecoder.SampleRate,
		BitDepth:    myArgs.OutBits, // Use targetBitDepth, not myDecoder.BitDepth
		NumChannels: myDecoder.NumChannels,
	}
	if myDecoder.Size != nil {
		myEncoder.Size = myDecoder.Size
	} else {
		myEncoder.Size = 0
	}

	if myArgs.OutPath != "" {
		myEncoder.Filename = myArgs.OutPath
	}
	err := myEncoder.Initialise()
	if err != nil {
		return nil, nil, err
	}
	return myDecoder, myEncoder, nil
}
