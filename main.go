package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/jessevdk/go-flags"
	"gonum.org/v1/gonum/stat"
	"gonum.org/v1/gonum/stat/distuv"
)

type Options struct {
	Baseline    string  `short:"b" long:"baseline" description:"Label for baseline series"`
	Experiment  string  `short:"e" long:"experiment" description:"Label for experiment series"`
	Confidence  float64 `long:"confidence" default:"0.95" description:"Confidence level for statistical tests"`
	Threshold   float64 `long:"threshold" default:"3.0" description:"Threshold for coloring difference values (percentage)"`
	NoColor     bool    `long:"no-color" description:"Disable colored output"`
	Args        struct {
		InputFile string `positional-arg-name:"FILE" description:"Input CSV file"`
	} `positional-args:"yes"`
}

type Measurement struct {
	Date  time.Time
	Value float64
	Label string
}

type TimeSeries struct {
	Label        string
	Measurements []Measurement
	MinDate      time.Time
	MaxDate      time.Time
}

type AnalysisResult struct {
	Mean               float64
	Count              int
	StdDev             float64
	ConfidenceInterval [2]float64
}

type TimeSegmentAnalysis struct {
	Benchmark   AnalysisResult
	Experiment  AnalysisResult
	Difference  float64
	PValue      float64
	Significant bool
}

func welchTTest(x, y []float64) (t, p float64) {
	nx := float64(len(x))
	ny := float64(len(y))

	if nx < 2 || ny < 2 {
		return 0, 1
	}

	meanX := stat.Mean(x, nil)
	meanY := stat.Mean(y, nil)
	varX := stat.Variance(x, nil)
	varY := stat.Variance(y, nil)

	t = (meanX - meanY) / math.Sqrt(varX/nx+varY/ny)

	df := math.Pow(varX/nx+varY/ny, 2) /
		(math.Pow(varX/nx, 2)/(nx-1) + math.Pow(varY/ny, 2)/(ny-1))

	dist := distuv.StudentsT{Nu: df}
	p = 2 * dist.Survival(math.Abs(t))

	return t, p
}

func getUniqueLabels(data []Measurement) []string {
	labelMap := make(map[string]struct{})
	for _, m := range data {
		labelMap[m.Label] = struct{}{}
	}

	labels := make([]string, 0, len(labelMap))
	for label := range labelMap {
		labels = append(labels, label)
	}
	return labels
}

func containsLabel(labels []string, target string) bool {
	for _, label := range labels {
		if label == target {
			return true
		}
	}
	return false
}

func readCSV(filename string) ([]Measurement, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.Comma = ';'

	// Skip header
	_, err = reader.Read()
	if err != nil {
		return nil, err
	}

	var measurements []Measurement
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		date, err := time.Parse("2006-01-02 15:04:05", record[0])
		if err != nil {
			return nil, err
		}

		value, err := strconv.ParseFloat(strings.TrimSpace(record[1]), 64)
		if err != nil {
			return nil, err
		}

		measurements = append(measurements, Measurement{
			Date:  date,
			Value: value,
			Label: record[2],
		})
	}

	return measurements, nil
}

func filterByLabel(data []Measurement, label string) TimeSeries {
	var filtered []Measurement
	minDate := time.Now()
	maxDate := time.Time{}

	for _, m := range data {
		if m.Label == label {
			filtered = append(filtered, m)
			if m.Date.Before(minDate) {
				minDate = m.Date
			}
			if m.Date.After(maxDate) {
				maxDate = m.Date
			}
		}
	}

	return TimeSeries{
		Label:        label,
		Measurements: filtered,
		MinDate:      minDate,
		MaxDate:      maxDate,
	}
}

func analyzeTimeSeries(benchmark, experiment TimeSeries, confidenceLevel float64) map[string]TimeSegmentAnalysis {
	results := make(map[string]TimeSegmentAnalysis)

	// Overall analysis
	results["overall"] = analyzeSegment(benchmark.Measurements, experiment.Measurements, confidenceLevel)

	// Hourly breakdown
	hourlyBench := groupByHour(benchmark.Measurements)
	hourlyExp := groupByHour(experiment.Measurements)

	for hour := 0; hour < 24; hour++ {
		if b, ok := hourlyBench[hour]; ok {
			if e, ok := hourlyExp[hour]; ok {
				results[fmt.Sprintf("hour_%02d", hour)] = analyzeSegment(b, e, confidenceLevel)
			}
		}
	}

	// Daily breakdown
	dailyBench := groupByDayOfWeek(benchmark.Measurements)
	dailyExp := groupByDayOfWeek(experiment.Measurements)

	for day := 0; day < 7; day++ {
		if b, ok := dailyBench[day]; ok {
			if e, ok := dailyExp[day]; ok {
				results[fmt.Sprintf("day_%d", day)] = analyzeSegment(b, e, confidenceLevel)
			}
		}
	}

	return results
}

func analyzeSegment(benchmark, experiment []Measurement, confidenceLevel float64) TimeSegmentAnalysis {
	benchValues := measurementsToValues(benchmark)
	expValues := measurementsToValues(experiment)

	benchMean := stat.Mean(benchValues, nil)
	expMean := stat.Mean(expValues, nil)

	benchStdDev := stat.StdDev(benchValues, nil)
	expStdDev := stat.StdDev(expValues, nil)

	_, pValue := welchTTest(benchValues, expValues)

	benchCI := confidenceInterval(benchValues, confidenceLevel)
	expCI := confidenceInterval(expValues, confidenceLevel)

	return TimeSegmentAnalysis{
		Benchmark: AnalysisResult{
			Mean:               benchMean,
			Count:              len(benchValues),
			StdDev:             benchStdDev,
			ConfidenceInterval: benchCI,
		},
		Experiment: AnalysisResult{
			Mean:               expMean,
			Count:              len(expValues),
			StdDev:             expStdDev,
			ConfidenceInterval: expCI,
		},
		Difference:  ((expMean - benchMean) / benchMean) * 100,
		PValue:      pValue,
		Significant: pValue < (1 - confidenceLevel),
	}
}

func measurementsToValues(measurements []Measurement) []float64 {
	values := make([]float64, len(measurements))
	for i, m := range measurements {
		values[i] = m.Value
	}
	return values
}

func groupByHour(measurements []Measurement) map[int][]Measurement {
	grouped := make(map[int][]Measurement)
	for _, m := range measurements {
		hour := m.Date.Hour()
		grouped[hour] = append(grouped[hour], m)
	}
	return grouped
}

func groupByDayOfWeek(measurements []Measurement) map[int][]Measurement {
	grouped := make(map[int][]Measurement)
	for _, m := range measurements {
		day := int(m.Date.Weekday())
		grouped[day] = append(grouped[day], m)
	}
	return grouped
}

func confidenceInterval(values []float64, confidenceLevel float64) [2]float64 {
	mean := stat.Mean(values, nil)
	stdErr := stat.StdDev(values, nil) / math.Sqrt(float64(len(values)))

	dist := distuv.StudentsT{Nu: float64(len(values) - 1)}
	tValue := dist.Quantile((1 + confidenceLevel) / 2)

	margin := tValue * stdErr
	return [2]float64{mean - margin, mean + margin}
}

func printResults(results map[string]TimeSegmentAnalysis, showHourly, showDaily bool, threshold float64, useColor bool) {
	green := color.New(color.FgGreen)
	red := color.New(color.FgRed)

	formatDifference := func(diff float64) string {
		if !useColor {
			return fmt.Sprintf("%.2f%%", diff)
		}

		if math.Abs(diff) <= threshold {
			return fmt.Sprintf("%.2f%%", diff)
		}

		if diff < 0 {
			return green.Sprintf("%.2f%%", diff)
		}
		return red.Sprintf("%.2f%%", diff)
	}

	overall := results["overall"]
	fmt.Println("\nOverall Analysis Results:")
	fmt.Println(strings.Repeat("-", 50))
	fmt.Printf("Benchmark mean: %.4f (n=%d)\n", overall.Benchmark.Mean, overall.Benchmark.Count)
	fmt.Printf("Experiment mean: %.4f (n=%d)\n", overall.Experiment.Mean, overall.Experiment.Count)
	fmt.Printf("Difference: %s\n", formatDifference(overall.Difference))
	fmt.Printf("P-value: %.4f\n", overall.PValue)

	if showHourly {
		fmt.Println("\nHourly Breakdown:")
		fmt.Println(strings.Repeat("-", 50))
		for hour := 0; hour < 24; hour++ {
			key := fmt.Sprintf("hour_%02d", hour)
			if analysis, ok := results[key]; ok {
				fmt.Printf("Hour %02d: %s\n", hour, formatDifference(analysis.Difference))
			}
		}
	}

	if showDaily {
		fmt.Println("\nDay of Week Breakdown:")
		fmt.Println(strings.Repeat("-", 50))
		days := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
		for day := 0; day < 7; day++ {
			key := fmt.Sprintf("day_%d", day)
			if analysis, ok := results[key]; ok {
				fmt.Printf("%s: %s\n", days[day], formatDifference(analysis.Difference))
			}
		}
	}
}

func isTerminal() bool {
	fileInfo, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}

func main() {
	var opts Options
	parser := flags.NewParser(&opts, flags.Default)
	parser.Usage = "[OPTIONS] FILE"

	_, err := parser.Parse()
	if err != nil {
		os.Exit(1)
	}

	if opts.Args.InputFile == "" {
		parser.WriteHelp(os.Stderr)
		os.Exit(1)
	}

	data, err := readCSV(opts.Args.InputFile)
	if err != nil {
		log.Fatalf("Error reading CSV: %v", err)
	}

	labels := getUniqueLabels(data)
	if len(labels) != 2 && (opts.Baseline == "" || opts.Experiment == "") {
		log.Fatalf("Found %d labels in data. When more than 2 labels exist, --baseline and --experiment must be specified.\nAvailable labels: %v",
			len(labels), strings.Join(labels, ", "))
	}

	if opts.Baseline == "" && opts.Experiment == "" {
		opts.Baseline = labels[0]
		opts.Experiment = labels[1]
		fmt.Printf("Auto-selected baseline: %s, experiment: %s\n", opts.Baseline, opts.Experiment)
	} else if opts.Baseline == "" {
		for _, label := range labels {
			if label != opts.Experiment {
				opts.Baseline = label
				fmt.Printf("Auto-selected baseline: %s\n", opts.Baseline)
				break
			}
		}
	} else if opts.Experiment == "" {
		for _, label := range labels {
			if label != opts.Baseline {
				opts.Experiment = label
				fmt.Printf("Auto-selected experiment: %s\n", opts.Experiment)
				break
			}
		}
	}

	if !containsLabel(labels, opts.Baseline) || !containsLabel(labels, opts.Experiment) {
		log.Fatalf("Specified labels not found in data. Available labels: %v", strings.Join(labels, ", "))
	}

	baseline := filterByLabel(data, opts.Baseline)
	experiment := filterByLabel(data, opts.Experiment)

	if len(baseline.Measurements) == 0 || len(experiment.Measurements) == 0 {
		log.Fatalf("No data found for one or both labels")
	}

	analysis := analyzeTimeSeries(baseline, experiment, opts.Confidence)

	timeRange := baseline.MaxDate.Sub(baseline.MinDate)
	showHourly := timeRange >= 24*time.Hour
	showDaily := timeRange >= 7*24*time.Hour

	useColor := !opts.NoColor && isTerminal()

	printResults(analysis, showHourly, showDaily, opts.Threshold, useColor)
}
