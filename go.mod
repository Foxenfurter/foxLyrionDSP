module github.com/Foxenfurter/foxLyrionDSP

go 1.23.6

require github.com/Foxenfurter/foxAudioLib v0.0.0

replace github.com/Foxenfurter/foxAudioLib => ../foxAudioLib // Local path replacement

require (
	github.com/argusdusty/gofft v1.2.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	golang.org/x/sys v0.31.0 // indirect
	gonum.org/v1/gonum v0.16.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	scientificgo.org/fft v0.0.0 // indirect
)
