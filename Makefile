GO	= go
EXT	=

PLAKAR	= plakar
VERSION	= v1.0.0

all: build

build:
	${GO} build -v -o b2Storage${EXT} ./plugin/storage

package: build
	rm -f backblaze_${VERSION}_*.ptar
	${PLAKAR} pkg create ./manifest.yaml ${VERSION}

uninstall:
	-${PLAKAR} pkg rm backblaze

install: package
	${PLAKAR} pkg add ./backblaze_${VERSION}_*.ptar

reinstall: uninstall install

test:
	${GO} test -v ./...

check: test

clean:
	rm -f b2Storage${EXT} backblaze_${VERSION}*.ptar

.PHONY: all build package uninstall install reinstall test check clean
