//go:build windows

package main

import "golang.org/x/sys/windows"

// Кодировка вывода в консоль.
//
// Go пишет в стандартный вывод UTF-8, а консоль Windows по умолчанию
// разбирает байты в кодовой странице 866. Русский текст в ней превращается в
// кашу — и первое, что видит администратор при установке, это нечитаемые
// строки. Одна системная функция чинит это целиком.
var (
	kernel32     = windows.NewLazySystemDLL("kernel32.dll")
	getOutputCP  = kernel32.NewProc("GetConsoleOutputCP")
	setOutputCP  = kernel32.NewProc("SetConsoleOutputCP")
	codePageUTF8 = uintptr(65001)
)

// настроитьКонсоль переводит вывод в UTF-8 и возвращает то, что вернёт всё
// обратно.
//
// Возвращает обязательно: кодовая страница живёт не в процессе, а в самом
// окне консоли, и оставить её изменённой значит сломать вывод следующей
// команде, которую наберёт человек.
func настроитьКонсоль() func() {
	было, _, _ := getOutputCP.Call()
	if было == codePageUTF8 || было == 0 {
		return func() {}
	}

	if ok, _, _ := setOutputCP.Call(codePageUTF8); ok == 0 {
		// Не удалось — не беда: вывод будет кашей, но работать всё будет.
		return func() {}
	}

	return func() { _, _, _ = setOutputCP.Call(было) }
}
