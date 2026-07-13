BINARY := photos
SRC    := photos.swift

.PHONY: build clean

build:
	swiftc $(SRC) -o $(BINARY) -framework Photos -framework Cocoa -framework CoreLocation

clean:
	rm -f $(BINARY)
