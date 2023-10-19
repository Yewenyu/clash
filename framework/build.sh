#! /bin/sh

# make PP=g++ \
#     CC=gcc \
#     CFLAGS="-mmacosx-version-min=10.14 -mios-version-min=11.0"
    # LFLAGS="-arch x86_64 -mmacosx-version-min=10.14 -mios-version-min=11.0 -Wl,-Bsymbolic-functions" static

# export PATH=$PATH:~/go/bin
go mod tidy
go install golang.org/x/mobile/cmd/gomobile@latest
go get golang.org/x/mobile/cmd/gobind
export PATH=$PATH:$(go env GOPATH)/bin
gomobile init
gomobile bind  -target=ios,macos,iossimulator -o=framework/Clash.xcframework -bootclasspath=.. -iosversion=11.0 -v
open framework/
# python3 build_clash_universal.py
