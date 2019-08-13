#!/bin/bash

export GOPATH=$PWD
export GO111MODULE=on
if [ ! -d /tmp/go ]; then
  cd /tmp
  if [ ! -f /tmp/go1.12.7.linux-amd64.tar.gz ]; then
    wget -q https://dl.google.com/go/go1.12.7.linux-amd64.tar.gz
  fi
  tar -xzf go1.12.7.linux-amd64.tar.gz
  rm /tmp/go1.12.7.linux-amd64.tar.gz
fi
cd ~/src
mkdir -p /tmp/pkg
if [ ! -L pkg ]; then
  ln -s /tmp/pkg .
fi
/tmp/go/bin/go build -buildmode=plugin -o ~/stderr.so stderr.go
OPENTELEMETRY_LIB=~/stderr.so /tmp/go/bin/go run main.go
