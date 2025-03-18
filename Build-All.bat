set GOOS=linux
set GOARCH=arm64
go build -o "C:/Users/jonat/go/build/foxLyrionDSP/linux-arm64/SqueezeDSP" foxLyrionDSP.go

set GOOS=windows
set GOARCH=amd64
go build  -o "C:/Users/jonat/go/build/foxLyrionDSP/windows/SqueezeDSP.exe" foxLyrionDSP.go
