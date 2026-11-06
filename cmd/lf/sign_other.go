//go:build !darwin

package main

func signForMac(string) error { return nil }
