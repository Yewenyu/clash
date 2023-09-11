#! /bin/sh

# go get golang.org/x/mobile

go get golang.org/x/mobile/cmd/gobind
go get golang.org/x/mobile/cmd/gomobile
export PATH=$PATH:~/go/bin
gomobile init
gomobile bind  -target=ios,macos,iossimulator -o=./framework/framework/Clash.xcframework

# python3 build_clash_universal.py