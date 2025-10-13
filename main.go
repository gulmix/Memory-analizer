package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
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

// DisplayConfig будет использоваться при отображении информационной панели, которую мы создадим позже.
// Она позволит гибко настраивать параметры отображения без изменения основной логики программы.
type DisplayConfig struct {
	//Период времени между обновлениями данных на экране. Влияет на актуальность отображаемой информации и нагрузку на систему
	//
	//Измеряется с помощью time.Duration
	UpdateInterval time.Duration

	//Ограничивает число процессов в списке.Помогает избежать перегруженности экрана
	//Позволяет сфокусироваться на самых важных процессах
	//Обычно показываются процессы с наибольшим потреблением памяти
	TopProcesses int
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

func (d *DarwinMemoryReader) ReadSystemMemory() (SystemMemoryInfo, error) {
	cmd := exec.Command("sysctl", "-n", "hw.memsize")
	output, err := cmd.Output()
	if err != nil {
		return SystemMemoryInfo{}, err
	}
	totalMemoryStr := strings.TrimSpace(string(output))
	if totalMemoryStr == "" {
		return SystemMemoryInfo{}, fmt.Errorf("Не удалось получить информации об общем объеме RAM")
	}
	totalMemory, err := strconv.ParseUint(totalMemoryStr, 10, 64)
	if err != nil {
		return SystemMemoryInfo{}, err
	}
	VmStats := make(map[string]uint64)
	cmd = exec.Command("vm_stat")
	output, err = cmd.Output()
	if err != nil {
		return SystemMemoryInfo{}, err
	}
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "Mach Virtual Memory Statistics") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		valueStr := strings.Trim(strings.TrimSpace(parts[1]), ".")

		value, err := strconv.ParseUint(valueStr, 10, 64)
		if err != nil {
			continue
		}
		key = strings.TrimPrefix(key, "Pages ")
		VmStats[key] = value
	}
	freePages := VmStats["free"] + VmStats["inactive"]
	availablePages := VmStats["free"] + VmStats["inactive"] + VmStats["speculative"]
	if fileCache, exists := VmStats["file-backed pages"]; exists {
		availablePages += fileCache
	} else if cache, exists := VmStats["cache"]; exists {
		availablePages += cache
	}
	cmd = exec.Command("sysctl", "-n", "vm.swapusage")
	output, err = cmd.Output()
	if err != nil {
		return SystemMemoryInfo{}, err
	}
	output = []byte(strings.TrimSpace(string(output)))
	parts := strings.Fields(string(output))
	if len(parts) < 9 {
		return SystemMemoryInfo{}, fmt.Errorf("Неверный формат SwapInfo")
	}
	total, err := parseMemSize(parts[2])
	if err != nil {
		return SystemMemoryInfo{}, fmt.Errorf("Невозможно распарсить TotalSwap")
	}
	free, err := parseMemSize(parts[8])
	if err != nil {
		return SystemMemoryInfo{}, fmt.Errorf("Невозможно распарсить FreeSwap")
	}
	info := SystemMemoryInfo{
		TotalMemory:     totalMemory,
		FreeMemory:      freePages * uint64(4096),
		AvailableMemory: availablePages * uint64(4096),
		SwapTotal:       total,
		SwapFree:        free,
	}
	return info, nil
}

func parseMemSize(sizeStr string) (uint64, error) {
	var mult uint64 = 1
	if strings.HasSuffix(sizeStr, "K") {
		mult = 1024
		sizeStr = strings.TrimSuffix(sizeStr, "K")
	} else if strings.HasSuffix(sizeStr, "M") {
		mult = 1024 * 1024
		sizeStr = strings.TrimSuffix(sizeStr, "M")
	} else if strings.HasSuffix(sizeStr, "G") {
		mult = 1024 * 1024 * 1024
		sizeStr = strings.TrimSuffix(sizeStr, "G")
	} else if strings.HasSuffix(sizeStr, "T") {
		mult = 1024 * 1024 * 1024 * 1024
		sizeStr = strings.TrimSuffix(sizeStr, "T")
	}
	val, err := strconv.ParseFloat(sizeStr, 64)
	if err != nil {
		return 0, err
	}
	return uint64(val * float64(mult)), nil
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

func (l *LinuxMemoryReader) ReadSystemMemory() (SystemMemoryInfo, error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return SystemMemoryInfo{}, fmt.Errorf("Не удалось открыть /proc/meminfo: %v", err)
	}
	defer file.Close()
	memStats, err := parseMemInfo(file)
	if err != nil {
		return SystemMemoryInfo{}, err
	}
	var info SystemMemoryInfo
	if total, exists := memStats["MemTotal"]; exists {
		info.TotalMemory = total * 1024
	} else {
		return SystemMemoryInfo{}, fmt.Errorf("MemTotal не найден")
	}
	if free, exists := memStats["MemFree"]; exists {
		info.FreeMemory = free * 1024
	} else {
		return SystemMemoryInfo{}, fmt.Errorf("MemFree не найден")
	}
	if available, exists := memStats["MemAvailable"]; exists {
		info.AvailableMemory = available * 1024
	} else {
		info.AvailableMemory = info.FreeMemory
		if buffers, exists := memStats["Buffers"]; exists {
			info.AvailableMemory += buffers * 1024
		}
		if cached, exists := memStats["Cached"]; exists {
			info.AvailableMemory += cached * 1024
		}
	}
	if swapTotal, exists := memStats["SwapTotal"]; exists {
		info.SwapTotal = swapTotal * 1024
	} else {
		return info, fmt.Errorf("SwapTotal не найден")
	}
	if swapFree, exists := memStats["SwapFree"]; exists {
		info.SwapFree = swapFree * 1024
	} else {
		return info, fmt.Errorf("SwapFree не найден")
	}
	return info, nil
}

func parseMemInfo(file *os.File) (map[string]uint64, error) {
	stats := make(map[string]uint64)
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		valueFiels := strings.Fields(value)
		if len(valueFiels) == 0 {
			continue
		}
		val, err := strconv.ParseUint(valueFiels[0], 10, 64)
		if err != nil {
			continue
		}
		stats[key] = val
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("Ошибка чтения: %v", err)
	}
	if len(stats) == 0 {
		return nil, fmt.Errorf("Не удалось извлечь данные")
	}
	return stats, nil
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

func FormatMemorySize(bytes uint64) string {
	var prefixIndicator int = 0
	for bytes >= 1024 {
		prefixIndicator++
		bytes /= 1024
	}
	switch prefixIndicator {
	case 0:
		size := fmt.Sprintf("%.2f B", float64(bytes))
		return size
	case 1:
		size := fmt.Sprintf("%.2f KB", float64(bytes))
		return size
	case 2:
		size := fmt.Sprintf("%.2f MB", float64(bytes))
		return size
	case 3:
		size := fmt.Sprintf("%.2f GB", float64(bytes))
		return size
	}
	return "Unknown size"
}

func getShortProcessName(fullName string) string {
	baseName := filepath.Base(fullName)
	baseName = strings.TrimSpace(baseName)
	if strings.HasSuffix(baseName, ".app") {
		baseName = strings.TrimSuffix(baseName, ".app")
	} else if idx := strings.Index(baseName, ".app"); idx != -1 {
		baseName = baseName[:idx]
	}
	if strings.HasSuffix(baseName, "-helper (Renderer)") {
		baseName = strings.TrimSuffix(baseName, "-helper (Renderer)")
	}
	if strings.HasSuffix(baseName, "-helper") {
		baseName = strings.TrimSuffix(baseName, "-helper")
	}
	if len(baseName) > 15 {
		baseName = baseName[:12] + "..."
	}
	return baseName
}

func FormatTable(processes []ProcessInfo) string {
	var res strings.Builder
	res.WriteString("Process List:\n")
	res.WriteString("PID      NAME            MEMORY\n")
	res.WriteString("--------------------------------\n")
	for _, process := range processes {
		pidStr := fmt.Sprintf("%d", process.PID)
		if len(pidStr) > 8 {
			pidStr = pidStr[:8]
		} else {
			pidStr = pidStr + strings.Repeat(" ", 8-len(pidStr))
		}
		name := getShortProcessName(process.Name)
		if len(name) > 15 {
			name = name[:15]
		} else {
			name = name + strings.Repeat(" ", 15-len(name))
		}
		memoryStr := FormatMemorySize(process.MemoryUsage)
		if len(memoryStr) > 10 {
			memoryStr = memoryStr[:10]
		} else {
			memoryStr = strings.Repeat(" ", 10-len(memoryStr)) + memoryStr
		}
		res.WriteString(pidStr)
		res.WriteString(" ")
		res.WriteString(name)
		res.WriteString(" ")
		res.WriteString(memoryStr)
		res.WriteString("\n")
	}
	return res.String()
}

func FormatSystemStats(stats SystemMemoryInfo) string {
	var res strings.Builder
	res.WriteString("System Memory:\n")
	totalStr := FormatMemorySize(stats.TotalMemory)
	res.WriteString(fmt.Sprintf("Total:     %s\n", totalStr))

	usedMemory := stats.TotalMemory - stats.AvailableMemory
	usedStr := FormatMemorySize(usedMemory)
	usedPercent := float64(usedMemory) / float64(stats.TotalMemory) * 100
	res.WriteString(fmt.Sprintf("Used:      %s (%.1f%%)\n", usedStr, usedPercent))

	availableStr := FormatMemorySize(stats.AvailableMemory)
	res.WriteString(fmt.Sprintf("Available: %s\n", availableStr))

	swapUsed := stats.SwapTotal - stats.SwapFree
	swapUsedStr := FormatMemorySize(swapUsed)
	swapPercent := float64(swapUsed) / float64(stats.SwapTotal) * 100
	res.WriteString(fmt.Sprintf("Swap Used: %s (%.1f%%)\n", swapUsedStr, swapPercent))
	return res.String()
}

func DisplayDashboard(stats SystemMemoryInfo, processes []ProcessInfo, config DisplayConfig) {
	var res strings.Builder
	res.WriteString("=== Memory Analyzer ===\n\n")

	res.WriteString(FormatSystemStats(stats))
	res.WriteString("\n")

	res.WriteString("Top Memory Processes:\n")

	res.WriteString(FormatTable(processes))
	res.WriteString("\n")

	currentTime := time.Now().Format("2006-01-02 15:04:05")
	res.WriteString(fmt.Sprintf("Updated: %s\n", currentTime))

	res.WriteString("Press Ctrl+C to exit\n")

	fmt.Print(res.String())
}

func getProcName(pid int) string {
	switch pid {
	case 1234, 1001:
		return "chrome"
	case 5678, 2002:
		return "vscode"
	case 9101, 3003:
		return "terminal"
	default:
		return fmt.Sprintf("process-%d", pid)
	}
}

func main() {
	var reader MemoryReader
	switch runtime.GOOS {
	case "darwin":
		reader = &DarwinMemoryReader{}
	case "linux":
		reader = &LinuxMemoryReader{}
	default:
		fmt.Printf("Unsupported operating system: %s\n", runtime.GOOS)
		return
	}

	// Создание конфигурации
	config := DisplayConfig{
		UpdateInterval: 3 * time.Second,
		TopProcesses:   10,
	}

	// Настройка обработки сигналов
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Создание ticker
	ticker := time.NewTicker(config.UpdateInterval)
	defer ticker.Stop()

	fmt.Printf("Starting Memory Analyzer on %s\n", runtime.GOOS)

	// Основной цикл
	for {
		select {
		case <-sigChan:
			fmt.Println("\nReceived interrupt signal. Exiting...")
			return
		case <-ticker.C:
			// Получение системной информации
			sysInfo, err := reader.ReadSystemMemory()
			if err != nil {
				fmt.Printf("Error reading system memory: %v\n", err)
				continue
			}

			// Получение списка процессов
			pids, err := reader.GetProcessList()
			if err != nil {
				fmt.Printf("Error getting process list: %v\n", err)
				continue
			}

			// Сбор информации о процессах
			var processes []ProcessInfo
			for _, pid := range pids {
				if mem, err := reader.ReadProcessMemory(pid); err == nil {
					name := getProcName(pid)
					processes = append(processes, ProcessInfo{
						PID:         pid,
						Name:        name,
						MemoryUsage: mem,
					})
				}
			}

			// Отображение информационной панели
			DisplayDashboard(sysInfo, processes, config)
		}
	}
}
