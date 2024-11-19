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

	"github.com/jessevdk/go-flags"
	"gonum.org/v1/gonum/stat"
	"gonum.org/v1/gonum/stat/distuv"
)

type Options struct {
	Baseline   string  `short:"b" long:"baseline" description:"Label for baseline series"`
	Experiment string  `short:"e" long:"experiment" description:"Label for experiment series"`
	Confidence float64 `long:"confidence" default:"0.95" description:"Confidence level for statistical tests"`
	Args       struct {
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

// welchTTest implements Welch's t-test for two samples
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

	// Calculate t-statistic
	t = (meanX - meanY) / math.Sqrt(varX/nx+varY/ny)

	// Calculate degrees of freedom using Welch–Satterthwaite equation
	df := math.Pow(varX/nx+varY/ny, 2) /
		(math.Pow(varX/nx, 2)/(nx-1) + math.Pow(varY/ny, 2)/(ny-1))

	// Create a Student's t-distribution with calculated degrees of freedom
	dist := distuv.StudentsT{Nu: df}

	// Calculate two-tailed p-value
	p = 2 * dist.Survival(math.Abs(t))

	return t, p
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

	// Read and parse data
	data, err := readCSV(opts.Args.InputFile)
	if err != nil {
		log.Fatalf("Error reading CSV: %v", err)
	}

	// Get unique labels from data
	labels := getUniqueLabels(data)
	if len(labels) != 2 && (opts.Baseline == "" || opts.Experiment == "") {
		log.Fatalf("Found %d labels in data. When more than 2 labels exist, --baseline and --experiment must be specified.\nAvailable labels: %v",
			len(labels), strings.Join(labels, ", "))
	}

	// Auto-select labels if not specified
	if opts.Baseline == "" && opts.Experiment == "" {
		opts.Baseline = labels[0]
		opts.Experiment = labels[1]
		fmt.Printf("Auto-selected baseline: %s, experiment: %s\n", opts.Baseline, opts.Experiment)
	} else if opts.Baseline == "" {
		// If only experiment is specified, use the other label as baseline
		for _, label := range labels {
			if label != opts.Experiment {
				opts.Baseline = label
				fmt.Printf("Auto-selected baseline: %s\n", opts.Baseline)
				break
			}
		}
	} else if opts.Experiment == "" {
		// If only baseline is specified, use the other label as experiment
		for _, label := range labels {
			if label != opts.Baseline {
				opts.Experiment = label
				fmt.Printf("Auto-selected experiment: %s\n", opts.Experiment)
				break
			}
		}
	}

	// Validate that selected labels exist in data
	if !containsLabel(labels, opts.Baseline) || !containsLabel(labels, opts.Experiment) {
		log.Fatalf("Specified labels not found in data. Available labels: %v", strings.Join(labels, ", "))
	}

	// Split data into baseline and experiment series
	baseline := filterByLabel(data, opts.Baseline)
	experiment := filterByLabel(data, opts.Experiment)

	if len(baseline.Measurements) == 0 || len(experiment.Measurements) == 0 {
		log.Fatalf("No data found for one or both labels")
	}

	// Perform analysis
	analysis := analyzeTimeSeries(baseline, experiment, opts.Confidence)

	// Determine time ranges and what breakdowns to show
	timeRange := baseline.MaxDate.Sub(baseline.MinDate)
	showHourly := timeRange >= 24*time.Hour
	showDaily := timeRange >= 7*24*time.Hour

	// Print results
	printResults(analysis, showHourly, showDaily)
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
	reader.Comma = ';' // Set delimiter to semicolon

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

	// Calculate t-statistic and p-value using Welch's t-test
	_, pValue := welchTTest(benchValues, expValues)

	// Calculate confidence intervals
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

	// Get t-value for given confidence level and degrees of freedom
	dist := distuv.StudentsT{Nu: float64(len(values) - 1)}
	tValue := dist.Quantile((1 + confidenceLevel) / 2)

	margin := tValue * stdErr
	return [2]float64{mean - margin, mean + margin}
}

func printResults(results map[string]TimeSegmentAnalysis, showHourly, showDaily bool) {
	// Print overall results
	overall := results["overall"]
	fmt.Println("\nOverall Analysis Results:")
	fmt.Println(strings.Repeat("-", 50))
	fmt.Printf("Benchmark mean: %.4f (n=%d)\n", overall.Benchmark.Mean, overall.Benchmark.Count)
	fmt.Printf("Experiment mean: %.4f (n=%d)\n", overall.Experiment.Mean, overall.Experiment.Count)
	fmt.Printf("Difference: %.2f%%\n", overall.Difference)
	fmt.Printf("P-value: %.4f\n", overall.PValue)
	if overall.Significant {
		fmt.Println("Result is STATISTICALLY SIGNIFICANT")
	} else {
		fmt.Println("Result is NOT statistically significant")
	}

	// Print hourly breakdown if applicable
	if showHourly {
		fmt.Println("\nHourly Breakdown:")
		fmt.Println(strings.Repeat("-", 50))
		for hour := 0; hour < 24; hour++ {
			key := fmt.Sprintf("hour_%02d", hour)
			if analysis, ok := results[key]; ok {
				significance := ""
				if analysis.Significant {
					significance = " (SIGNIFICANT)"
				}
				fmt.Printf("Hour %02d: %.2f%%%s\n", hour, analysis.Difference, significance)
			}
		}
	}

	// Print daily breakdown if applicable
	if showDaily {
		fmt.Println("\nDay of Week Breakdown:")
		fmt.Println(strings.Repeat("-", 50))
		days := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
		for day := 0; day < 7; day++ {
			key := fmt.Sprintf("day_%d", day)
			if analysis, ok := results[key]; ok {
				significance := ""
				if analysis.Significant {
					significance = " (SIGNIFICANT)"
				}
				fmt.Printf("%s: %.2f%%%s\n", days[day], analysis.Difference, significance)
			}
		}
	}
}
