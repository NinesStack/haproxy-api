#!/bin/bash

die() {
	echo $1
	exit 1
}

file ../haproxy-api | grep "ELF.*LSB" || die "../haproxy-api is missing or not a Linux binary"

test -f haproxy.toml || cp haproxy.docker.toml haproxy.toml
cp ../haproxy-api . && cp -pr ../templates . && docker build -t haproxy-api . || die "Failed to build"
