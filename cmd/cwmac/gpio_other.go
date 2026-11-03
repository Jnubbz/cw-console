//go:build !linux

package main

import "errors"

func openGPIOKey(pin int) (keyLine, error)  { return nil, errors.New("no GPIO on this computer") }
func gpioPorts() []portInfo                 { return nil }
func parseGPIOPort(port string) (int, bool) { return 0, false }
