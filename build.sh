#! /bin/sh


go get golang.org/x/mobile
gomobile init
gomobile bind  -target=ios,macos,iossimulator -o=./framework/framework/Clash.xcframework

# python3 build_clash_universal.py
