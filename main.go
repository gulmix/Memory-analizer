package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"unicode"
)

// MemoryReader определяет все необходимые методы для работы с процессами
type MemoryReader interface {
	//ReadSystemMemory собирает информацию о полной, свободной и доступной памяти системы
	//
	//Возвращает структуру SystemMemoryInfo с информацией о системной памяти
	//В случае ошибки возвращает нулевую структуру и описание ошибки
	ReadSystemMemory() (SystemMemoryInfo, error)

	//GetProcessList собирает список всех запущенных процессов в системе
	//
	//Возвращает слайс PID всех активных процессов
	//В случае ошибки возвращает nil и описание ошибки
	GetProcessList() ([]int, error)

	//ReadProcessMemory возвращает количество используемой, определенным процессом, резидентной памяти в байтах
	//
	//Принимает PID процесса
	//В случае ошибки возвращает 0 и описание ошибки
	ReadProcessMemory(pid int) (uint64, error)
}

type DarwinMemoryReader struct{}

type LinuxMemoryReader struct{}

type SystemMemoryInfo struct {
	TotalMemory     uint64
	FreeMemory      uint64
	AvailableMemory uint64
	SwapTotal       uint64
	SwapFree        uint64
}

type ProcessInfo struct {
	PID         int
	Name        string
	MemoryUsage uint64
}

func (d *DarwinMemoryReader) GetProcessList() ([]int, error) {
	cmd := exec.Command("ps", "-e", "-o", "pid=")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var pids []int
	scanner := bufio.NewScanner(strings.NewReader(string(output)))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err != nil {
			continue
		}
		pids = append(pids, pid)
	}
	return pids, nil
}

func (d *DarwinMemoryReader) ReadProcessMemory(pid int) (uint64, error) {
	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "rss=")
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	rssStr := strings.TrimSpace(string(output))
	if rssStr == "" {
		return 0, fmt.Errorf("Процесс с pid %d не найден", pid)
	}

	rssKb, err := strconv.ParseUint(rssStr, 10, 64)
	if err != nil {
		return 0, err
	}

	return rssKb * 1024, nil
}

func (l *LinuxMemoryReader) GetProcessList() ([]int, error) {
	var pids []int

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !isAllDigits(name) {
			continue
		}

		pid, err := strconv.Atoi(name)
		if err != nil {
			continue
		}

		if pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func main() {
	reader := &DarwinMemoryReader{}

	// Получаем список процессов
	pids, err := reader.GetProcessList()
	if err != nil {
		fmt.Printf("Error getting process list: %v\n", err)
		return
	}

	// Проверяем память первых 3 процессов
	for i, pid := range pids {
		if i >= 3 {
			break
		}

		memory, err := reader.ReadProcessMemory(pid)
		if err != nil {
			fmt.Printf("Error reading memory for PID %d: %v\n", pid, err)
			continue
		}

		fmt.Printf("Process %d uses %d bytes (%d MB) of memory\n",
			pid, memory, memory/(1024*1024))
	}
}
