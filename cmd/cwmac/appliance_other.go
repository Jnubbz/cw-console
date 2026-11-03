//go:build !linux

package main

import (
	"errors"
	"time"
)

func syncDisk()                  {}
func setClock(t time.Time) error { return errors.New("not supported") }
func powerOff()                  {}
func restartHotspot() error      { return errors.New("not supported") }

func saveLastClock() {}

func mountBoot() error   { return errors.New("not supported") }
func unmountBoot()       {}
func trialReboot() error { return errors.New("not supported") }
