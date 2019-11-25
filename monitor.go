// MIT License
//
// Copyright (c) 2019 Peter Morjan
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package main

import (
	"bufio"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	tb "github.com/nsf/termbox-go"
	"github.com/pmorjan/abool"
	"golang.org/x/sys/unix"
)

var (
	delay     time.Duration
	batchmode = flag.Bool("b", false, "batch mode")
	version   = flag.Bool("v", false, "show version")
	help      = flag.Bool("h", false, "print this help")
	debug     abool.AtomicBool
	blue      = color.New(color.FgBlue).SprintFunc()
	cyan      = color.New(color.FgCyan).SprintFunc()
	green     = color.New(color.FgGreen).SprintfFunc()
	magenta   = color.New(color.FgMagenta).SprintfFunc()
	red       = color.New(color.FgRed).SprintfFunc()
	rootdev   = getRootdev()
	GitCommit string
	BuildDate string
)

func usage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintf(os.Stderr, "  %s [options]\n\n", filepath.Base(os.Args[0]))
	fmt.Fprintln(os.Stderr, "Options:")
	flag.PrintDefaults()
	specialKeys()
}

func specialKeys() {
	fmt.Fprintln(os.Stderr, "\nSpecial Keys:")
	fmt.Fprintln(os.Stderr, "  + : increase refresh interval")
	fmt.Fprintln(os.Stderr, "  - : decrease refresh interval")
	fmt.Fprintln(os.Stderr, "  d : toggle debug info")
	fmt.Fprintln(os.Stderr, "  c : run stress-ng cpu on one thread for 10 sec")
	fmt.Fprintln(os.Stderr, "  C : run stress-ng cpu on all threads for 10 sec")
	fmt.Fprintln(os.Stderr, "  m : run stress-ng matrix on one threads for 10 sec")
	fmt.Fprintln(os.Stderr, "  M : run stress-ng matrix on all threads for 10 sec")
	fmt.Fprintln(os.Stderr, "  r : reset min/max counters")
	fmt.Fprintln(os.Stderr, "  h : help")
	fmt.Fprintln(os.Stderr, "  q : quit")
}

func main() {
	log.SetFlags(0)
	flag.Usage = usage
	flag.Parse()
	if *version {
		fmt.Printf("GitCommit:%s BuildDate:%s GoVersion:%s\n", GitCommit, strings.Replace(BuildDate, "_", " ", -1), runtime.Version())
		os.Exit(0)
	}
	if *help || len(flag.Args()) > 0 {
		flag.Usage()
		os.Exit(0)
	}
	if *batchmode {
		fmt.Print(str())
		os.Exit(0)
	}
	if err := tb.Init(); err != nil {
		panic(err)
	}
	fmt.Print("\033[?25l") // hide cursor

	defer tb.Close()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, unix.SIGTERM, unix.SIGINT, unix.SIGQUIT)
	go func() {
		for range sigChan {
			tb.Close()
			fmt.Print("\033[?25h") // unhide cursor
			os.Exit(0)
		}
	}()

	keyChan := make(chan rune, 1)
	go keyHandler(keyChan)
	// defer profile.Start(profile.NoShutdownHook, profile.MemProfile).Stop()

	interval := time.Second
	tick := time.NewTicker(interval)
Loop:
	for {
		s := str()
		fmt.Print("\033[1;1H") // position cursor
		fmt.Print("\033[2J")   // clear screen
		fmt.Printf("%s Refresh Interval: %v (? for more options)\n\n", time.Now().Format("15:04:05"), interval)
		fmt.Print(s)
		select {
		case <-tick.C:
		case r := <-keyChan:
			switch r {
			case 'q':
				break Loop
			case '-':
				if interval > 10*time.Millisecond {
					interval = interval / 2
					tick.Stop()
					tick = time.NewTicker(interval)
				}
			case '+':
				if interval < 10*time.Second {
					interval = interval * 2
					tick.Stop()
					tick = time.NewTicker(interval)
				}
			case 'h', '?':
				fmt.Print("\033[1;1H") // position cursor
				fmt.Print("\033[2J")   // clear screen
				specialKeys()
				<-keyChan
			}
		}
	}
}

func keyHandler(keyChan chan<- rune) {
	for {
		ev := tb.PollEvent()
		if ev.Type != tb.EventKey {
			continue
		}
		k := ev.Ch
		if ev.Key == tb.KeyEsc || ev.Key == tb.KeyCtrlC {
			k = 'q'
		}
		switch k {
		case 'd':
			debug.Toggle()
		case 'c':
			go exec.Command("/usr/bin/stress-ng", "--cpu", "1", "--timeout", "10s").Run()
		case 'C':
			go exec.Command("/usr/bin/stress-ng", "--cpu", "0", "--timeout", "10s").Run()
		case 'm':
			go exec.Command("/usr/bin/stress-ng", "--matrix", "1", "--timeout", "10s").Run()
		case 'M':
			go exec.Command("/usr/bin/stress-ng", "--matrix", "0", "--timeout", "10s").Run()
		case 'r':
			resetFreqBuffer()
		default:
			keyChan <- k
		}
	}
}

func timed(f func() string) string {
	if !debug.Get() {
		return f()
	}
	t0 := time.Now()
	s := f()
	dt := time.Since(t0).Truncate(time.Microsecond)
	return fmt.Sprintf(" (%v)\n%s", dt, s)
}

func str() string {
	s := timed(cpuinfo)
	s += "Memory [MiB]\n" + timed(meminfo) + "\n"
	s += "Load average\n" + timed(loadAvg) + "\n"
	s += "Root disk\n" + timed(df) + "\n\n"
	s += "Sensors\n" + timed(sensors)
	return s
}

func read(path string) string {
	f, err := os.Open(path)
	if err != nil {
		if _, ok := err.(*os.PathError); ok {
			return ""
		}
		return err.Error()
	}
	defer f.Close()
	buf := make([]byte, 128)
	n, err := f.Read(buf)
	if err != nil {
		if _, ok := err.(*os.PathError); ok {
			return ""
		}
		return err.Error()
	}
	return strings.TrimSpace(string(buf[:n]))
}

func sensors() string {
	var str string
	const dir = "/sys/class/hwmon"
	dirs, err := ioutil.ReadDir(dir)
	if err != nil {
		if _, ok := err.(*os.PathError); ok {
			return ""
		}
		log.Fatal(err)
	}

	var temps []Temp
	var fans []Fan
	for _, d := range dirs {
		subdir := filepath.Join(dir, d.Name())
		if d.Mode()&os.ModeSymlink == 0 {
			continue
		}
		namefile := filepath.Join(subdir, "name")
		moduleName := read(namefile)
		switch moduleName {
		case "acpitz", "nct6795", "nct6776", "thinkpad", "nouveau", "k10temp", "coretemp":
			t, f := tempsAndFans(subdir, moduleName)
			temps = append(temps, t...)
			fans = append(fans, f...)
		case "radeon":
			temps = append(temps, temp1(subdir, "GPU"))
		case "iwlwifi":
			temps = append(temps, temp1(subdir, "WiFi"))
		default:
			if debug.Get() {
				log.Printf("# unknown module %s\n", moduleName)
				log.Printf(unknown(subdir))
			}
		}
	}
	n := calcMinInt(len(temps), len(fans))
	var i int
	for i = 0; i < n; i++ {
		str += fmt.Sprintf(" %s     %s\n", temps[i], fans[i])
	}
	for i := i; i < len(temps); i++ {
		str += fmt.Sprintf(" %s\n", temps[i])
	}
	for i := i; i < len(fans); i++ {
		str += fmt.Sprintf(" %s\n", fans[i])
	}
	return str
}

func unknown(dir string) string {
	var str string
	files, _ := filepath.Glob(dir + "/*input")
	for _, i := range files {
		label := filepath.Base(strings.TrimRight(i, "_input"))
		str += fmt.Sprintf(" %-14s   %s\n", label, read(i))
	}
	return str
}

func temp1(dir, label string) Temp {
	p := filepath.Join(dir, "temp1_input")
	return Temp{label: label, value: read(p)}
}

func tempsAndFans(dir, module string) (temps []Temp, fans []Fan) {
	files, _ := filepath.Glob(dir + "/*input")
	for _, i := range files {
		if strings.HasPrefix(i, dir+"/fan") {
			label := filepath.Base(strings.TrimRight(i, "_input"))
			fans = append(fans, Fan{label, read(i)})
			continue
		}
		if strings.HasPrefix(i, dir+"/temp") {
			l := strings.TrimRight(i, "_input") + "_label"
			value := read(i)
			if value == "" || value == "0" || string(value[0]) == "-" {
				continue
			}
			t := Temp{module, read(l), value}
			temps = append(temps, t)
		}
	}
	return
}

type Temp struct {
	module string
	label  string
	value  string
}

func (t Temp) String() string {
	if t.label == "" {
		t.label = t.module
	}
	if t.value == "" {
		t.value = "    -"
	} else {
		i, err := strconv.Atoi(t.value)
		if err != nil {
			t.value = err.Error()
		} else {
			t.value = fmt.Sprintf("%5.1f", float64(i)/1000)
		}
	}
	return fmt.Sprintf("%-14s   %s °C", t.label, blue(t.value))
}

type Fan struct {
	label string
	value string
}

func (f Fan) String() string {
	if f.value == "" {
		f.value = "-"
	}
	return fmt.Sprintf("%-10s   %s rpm", f.label, magenta("%4s", f.value))
}

func loadAvg() string {
	// 1    2    3    4 5   6
	// 0.00 0.12 0.09 1/371 4461
	// 123) 1, 5, and 15 minutes.
	//   4) currently runnable kernel scheduling entities (processes, threads).
	//   5) number of kernel sched‐ uling  entities that currently exist on the system.
	//   6) The fifth field is the PID of the process that was most recently created on the system.
	bytes, err := ioutil.ReadFile("/proc/loadavg")
	if err != nil {
		return ""
	}
	f := strings.Fields(string(bytes))
	return fmt.Sprintf(" %s %s %s %s %s\n", cyan(f[0]), cyan(f[1]), cyan(f[2]), f[3], f[4])
}

var (
	fileCpuinfo *os.File
	mu          sync.Mutex
	minFreq     = make(map[int]float64)
	maxFreq     = make(map[int]float64)
)

func resetFreqBuffer() {
	mu.Lock()
	defer mu.Unlock()
	for i := range minFreq {
		minFreq[i] = 0
	}
	for i := range maxFreq {
		maxFreq[i] = 0
	}
}

func cpuinfo() string {
	var err error
	const (
		header = "Core          0         1         2         3         4  GHz    Min  Max"
		footer = "              0         1         2         3         4  GHz    Min  Max"
	)
	if fileCpuinfo == nil {
		fileCpuinfo, err = os.Open("/proc/cpuinfo")
		if err != nil {
			log.Fatal(err)
		}
	}
	unix.Seek(int(fileCpuinfo.Fd()), 0, 0)
	scanner := bufio.NewScanner(fileCpuinfo)
	str := fmt.Sprintln(header)

	// processor	: 1
	// cpu MHz		: 2103.707
	// cache size	: 512 KB
	// physical id	: 0
	// core id		: 1
	// cpu cores	: 8
	var cores = make(map[int]string)
	var coreIdStr string
	var mhzStr string
	for scanner.Scan() {
		s := scanner.Text()
		if strings.HasPrefix(s, "core id") {
			coreIdStr = s
			continue
		}

		if strings.HasPrefix(s, "cpu MHz") {
			mhzStr = s
			continue
		}
		if strings.HasPrefix(s, "flags") {
			if coreIdStr == "" || mhzStr == "" {
				log.Fatal("values for core id or cpu MHz empty")
			}
			field := strings.Split(coreIdStr, ":")
			core_id, err := strconv.Atoi(strings.TrimSpace(field[1]))
			if err != nil {
				log.Fatalf("parse error: %v", err)
			}

			if _, ok := cores[core_id]; ok {
				// count cores only once
				continue
			}

			field = strings.Split(mhzStr, ":")
			mhz, err := strconv.ParseFloat(strings.TrimSpace(field[1]), 64)
			if err != nil {
				log.Fatalf("parse error: %v", err)
			}
			x := int(math.Round(mhz / 100))
			bar := green(strings.Repeat("#", x))
			if x < 40 {
				bar += strings.Repeat(" ", 39-x) + "|       "
			} else {
				bar += strings.Repeat(" ", 47-x)
			}

			cores[core_id] = fmt.Sprintf("%s MHz |%s", red("%4.0f", mhz), bar)
			mu.Lock()
			minFreq[core_id] = calcMinFreq(minFreq[core_id], mhz)
			maxFreq[core_id] = calcMaxFreq(maxFreq[core_id], mhz)
			mu.Unlock()
			coreIdStr = ""
			mhzStr = ""
		}
	}
	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}

	mu.Lock()
	for i := 0; i < len(cores); i++ {
		str += fmt.Sprintf(" %2d: %s  %s %s\n", i, cores[i], green("%4.0f", minFreq[i]), red("%4.0f", maxFreq[i]))
	}
	mu.Unlock()
	str += fmt.Sprintln(footer)
	return str
}

var reMemory = regexp.MustCompile(":?\\s+")

func meminfo() string {
	mem := make(map[string]int)
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return err.Error()
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var err error
		line := scanner.Text()
		fields := reMemory.Split(line, 3)
		if len(fields) != 3 {
			continue
		}
		switch fields[0] {
		case "MemTotal", "MemFree", "MemAvailable", "SwapCached", "SwapTotal", "SwapFree":
			mem[fields[0]], err = strconv.Atoi(fields[1])
			if err != nil {
				continue
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err.Error()
	}

	return fmt.Sprintf(" total:%s free:%s available:%s swap:%s\n",
		cyan(fmt.Sprintf("%d", mem["MemTotal"]/1024)),
		cyan(fmt.Sprintf("%d", mem["MemFree"]/1024)),
		cyan(fmt.Sprintf("%d", mem["MemAvailable"]/1024)),
		cyan(fmt.Sprintf("%d", (mem["SwapTotal"]-mem["SwapFree"])/1024)),
	)
}

func getRootdev() string {
	var dev string
	f, err := os.Open("/proc/mounts")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, " / ") {
			dev = strings.Fields(line)[0]
			break
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}
	return dev
}

func df() string {
	const dir = "/"
	var stat unix.Statfs_t
	unix.Statfs(dir, &stat)
	size := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bfree * uint64(stat.Bsize)
	used := size - free

	return fmt.Sprintf(" %s size:%s  used:%s  free:%s",
		rootdev,
		humanBytes(size),
		humanBytes(used),
		humanBytes(free),
	)
}

func calcMinInt(a, b int) int {
	if b < a {
		return b
	}
	return a
}

func calcMinFreq(a, b float64) float64 {
	if a == 0 {
		return b
	}
	if b == 0 {
		return a
	}
	if b < a {
		return b
	}
	return a
}

func calcMaxFreq(a, b float64) float64 {
	if a == 0 {
		return b
	}
	if b == 0 {
		return a
	}
	if b > a {
		return b
	}
	return a
}

func humanBytes(val uint64) string {
	units := []string{"B", "KB", "MB", "GB", "TB", "PB", "EB"}
	var i int
	var target uint64
	for i = range units {
		target = 1 << uint(10*(i+1))
		if val < target {
			break
		}
	}
	if i > 0 {
		return cyan(fmt.Sprintf("%0.1f ", float64(val)/(float64(target)/1024))) + units[i]
	}
	return fmt.Sprintf("%dB", val)
}
