#! /bin/sh


# export PATH=$PATH:~/go/bin
go mod tidy
go install golang.org/x/mobile/cmd/gomobile@latest
go get golang.org/x/mobile/cmd/gobind
gomobile init
gomobile bind  -target=ios,macos,iossimulator -o=framework/Clash.xcframework -bootclasspath=.. -v

# python3 build_clash_universal.py
