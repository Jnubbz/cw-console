//go:build !linux

package main

func watchRigHID()      {}
func hidStatus() string { return "" }
