set GOOS=windows
set GOARCH=amd64
set GOEXPERIMENT=jsonv2

set CGO_ENABLED=0

set CGO_CFLAGS="-I%VCPATH%\include"

set CC=gcc
set CGO_LDFLAGS="-L./libs/windows_amd64 -lrustfft -lws2_32 -luserenv -lbcrypt -lntdll"

go build -ldflags="-s -w" -o "C:/Users/jonat/go/build/foxLyrionDSP/test/SqueezeDSPTest.exe" foxLyrionDSP.go
:: go build -ldflags="-s -w" -gcflags="all=-N -l" -o "C:/Users/jonat/go/build/foxLyrionDSP/test/SqueezeDSPTest.exe" foxLyrionDSP.go


set CGO_ENABLED=0
go build -o "C:/Users/jonat/go/build/foxLyrionDSP/test/dsp_tester.exe" dsp_tester.go
