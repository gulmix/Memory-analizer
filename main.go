package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

func (l *LinuxMemoryReader) ReadProcessMemory(pid int) (uint64, error) {
	pathName := filepath.Join("proc", strconv.Itoa(pid), "status")
	file, err := os.Open(pathName)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "VmRSS:") {
			val, err := extractValue(line)
			if err != nil {
				return 0, err
			}
			return val * 1024, err
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("Ошибка при читении файла: %v", err)
	}
	return 0, fmt.Errorf("VmRSS не найден для PID %d", pid)
}

func extractValue(line string) (uint64, error) {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("Неверный формат строки")
	}
	valueStr := strings.TrimSpace(parts[1])
	valueStr = strings.TrimSuffix(valueStr, "kB")
	valueStr = strings.TrimSpace(valueStr)
	val, err := strconv.ParseUint(valueStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("Не удалось конвертировать значение VmRSS: %s", valueStr)
	}
	return val, nil
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
	// Создаём тестовый экземпляр структуры
	sysInfo := SystemMemoryInfo{
		TotalMemory:     16 * 1024 * 1024 * 1024, // 16 GB
		FreeMemory:      4 * 1024 * 1024 * 1024,  // 4 GB
		AvailableMemory: 6 * 1024 * 1024 * 1024,  // 6 GB
		SwapTotal:       8 * 1024 * 1024 * 1024,  // 8 GB
		SwapFree:        7 * 1024 * 1024 * 1024,  // 7 GB
	}

	// Выводим информацию в разных форматах
	fmt.Println("System Memory Information:")
	fmt.Printf("Total Memory:     %.2f GB\n",
		float64(sysInfo.TotalMemory)/(1024*1024*1024))
	fmt.Printf("Free Memory:      %.2f GB\n",
		float64(sysInfo.FreeMemory)/(1024*1024*1024))
	fmt.Printf("Available Memory: %.2f GB\n",
		float64(sysInfo.AvailableMemory)/(1024*1024*1024))
	fmt.Printf("Swap Total:       %.2f GB\n",
		float64(sysInfo.SwapTotal)/(1024*1024*1024))
	fmt.Printf("Swap Free:        %.2f GB\n",
		float64(sysInfo.SwapFree)/(1024*1024*1024))

	// Вычисляем и выводим дополнительную информацию
	usedMemory := sysInfo.TotalMemory - sysInfo.AvailableMemory
	usedSwap := sysInfo.SwapTotal - sysInfo.SwapFree

	fmt.Printf("\nDerived Information:\n")
	fmt.Printf("Memory Usage: %.1f%%\n",
		float64(usedMemory)/float64(sysInfo.TotalMemory)*100)
	fmt.Printf("Swap Usage:   %.1f%%\n",
		float64(usedSwap)/float64(sysInfo.SwapTotal)*100)
}
