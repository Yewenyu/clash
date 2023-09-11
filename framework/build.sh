#! /bin/sh

# make PP=g++ \
#     CC=gcc \
#     CFLAGS="-mmacosx-version-min=10.14 -mios-version-min=11.0"
    # LFLAGS="-arch x86_64 -mmacosx-version-min=10.14 -mios-version-min=11.0 -Wl,-Bsymbolic-functions" static

go get golang.org/x/mobile
gomobile init
gomobile bind  -target=ios,macos,iossimulator -o=framework/Clash.xcframework -bootclasspath=.. -iosversion=11.0 -v

# python3 build_clash_universal.py
