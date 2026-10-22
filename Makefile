# Build and run the server, build and flash the firmware:
#   make              build both
#   make run          build and start the server
#   make flash        build the firmware and write it to the board (PORT=… for another port)
# The firmware targets load ESP-IDF from IDF_EXPORT, unless idf.py already works in this terminal.

SHELL := /bin/bash
PORT ?= /dev/ttyACM0
IDF_EXPORT ?= $(HOME)/esp/esp-idf/export.sh
IDF = $(if $(shell command -v idf.py),,. $(IDF_EXPORT) >/dev/null &&) idf.py

.PHONY: all server run firmware flash

all: server firmware

server:
	cd server && go build

run: server
	cd server && ./tabula

firmware:
	cd firmware && $(IDF) build

flash:
	cd firmware && $(IDF) -p $(PORT) flash
