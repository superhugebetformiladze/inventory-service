//go:build !windows

package main

// На остальных системах консоль и так в UTF-8.
func настроитьКонсоль() func() { return func() {} }
