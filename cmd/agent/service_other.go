//go:build !windows

package main

import "errors"

// Заглушки для не-Windows.
//
// Файл существует не ради полноты: агент собирается и запускается в
// контейнере с Linux, и без этих заглушек проверить главный цикл, буфер и
// задания было бы негде — а именно они, а не служебная обвязка, содержат
// почти всю логику.

var errТолькоWindows = errors.New("служба ставится только на Windows")

func служебныйЗапуск() bool { return false }

func runAsService() error { return errТолькоWindows }

func командаInstall(_ []string) error { return errТолькоWindows }

func командаUninstall(_ []string) error { return errТолькоWindows }
