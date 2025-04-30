:: Force pure Go compilation (no cgo)
set CGO_ENABLED=0

:: Linux builds
set GOOS=linux
set GOARCH=arm64
go build -o "C:/Users/jonat/go/build/foxLyrionDSP/publishlinux-arm64/SqueezeDSP" foxLyrionDSP.go

set GOOS=linux
set GOARCH=arm
set GOARM=7
go build -o "C:/Users/jonat/go/build/foxLyrionDSP/publishlinux-arm/SqueezeDSP" foxLyrionDSP.go

set GOOS=linux
set GOARCH=amd64
go build -o "C:/Users/jonat/go/build/foxLyrionDSP/publishlinux-x64/SqueezeDSP" foxLyrionDSP.go

:: Windows build
set GOOS=windows
set GOARCH=amd64
go build -o "C:/Users/jonat/go/build/foxLyrionDSP/publishWin64/SqueezeDSP.exe" foxLyrionDSP.go

:: macOS builds
set GOOS=darwin
set GOARCH=amd64
go build -o "C:/Users/jonat/go/build/foxLyrionDSP/publishOsx-x64/SqueezeDSP" foxLyrionDSP.go

set GOOS=darwin
set GOARCH=arm64
go build -o "C:/Users/jonat/go/build/foxLyrionDSP/publishOSX-arm64/SqueezeDSP" foxLyrionDSP.go
