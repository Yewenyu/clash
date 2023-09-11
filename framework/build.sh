#! /bin/sh


go get golang.org/x/mobile
export PATH=$PATH:~/go/bin
gomobile init
gomobile bind  -target=ios,macos,iossimulator -o=framework/Clash.xcframework -bootclasspath=.. -v

# python3 build_clash_universal.py
