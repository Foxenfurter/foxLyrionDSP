set GOOS=windows
set GOARCH=amd64
go build -gcflags="all=-N -l" -o "C:/Users/jonat/go/build/foxLyrionDSP/test/SqueezeDSPTest.exe" foxLyrionDSP.go
go build -o "C:/Users/jonat/go/build/foxLyrionDSP/test/dsp_tester.exe" dsp_tester.go