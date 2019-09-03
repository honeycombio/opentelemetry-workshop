package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc/codes"

	"go.opentelemetry.io/api/key"
	"go.opentelemetry.io/api/metric"
	"go.opentelemetry.io/api/registry"
	"go.opentelemetry.io/api/tag"
	"go.opentelemetry.io/api/trace"
	"go.opentelemetry.io/plugin/httptrace"

	_ "go.opentelemetry.io/experimental/streaming/exporter/stderr/install"
)

var (
	tracer = trace.GlobalTracer().
		WithService("server").
		WithComponent("main").
		WithResources(
			key.New("whatevs").String("nooooo"),
		)
	appKey         = key.New("honeycomb.io/glitch/app", registry.WithDescription("The Glitch app name."))
	containerKey   = key.New("honeycomb.io/glitch/container_id", registry.WithDescription("The Glitch container id."))
	diskUsedMetric = metric.NewFloat64Gauge("honeycomb.io/glitch/disk_usage",
		metric.WithKeys(appKey, containerKey),
		metric.WithDescription("Amount of disk used."),
	)
	diskQuotaMetric = metric.NewFloat64Gauge("honeycomb.io/glitch/disk_quota",
		metric.WithKeys(appKey, containerKey),
		metric.WithDescription("Amount of disk quota available."),
	)
	meter = metric.GlobalMeter()
)

func dbHandler(ctx context.Context, color string) int {
	ctx, span := tracer.Start(ctx, "database")
	defer span.Finish()

	// Pretend we talked to a database here.
	return 0
}

func restartHandler(w http.ResponseWriter, req *http.Request) {
	os.Exit(0)
}

func rootHandler(w http.ResponseWriter, req *http.Request) {
	attrs, tags, spanCtx := httptrace.Extract(req)

	req = req.WithContext(tag.WithMap(req.Context(), tag.NewMap(tag.MapUpdate{
		MultiKV: tags,
	})))

	ctx, span := tracer.Start(
		req.Context(),
		"root",
		trace.WithAttributes(attrs...),
		trace.ChildOf(spanCtx),
	)
	defer span.Finish()

	span.AddEvent(ctx, "annotation within span")
	_ = dbHandler(ctx, "foo")

	fmt.Fprintf(w, "Click [Tools] > [Logs] to see spans!")
}

func fibHandler(w http.ResponseWriter, req *http.Request) {
	attrs, tags, spanCtx := httptrace.Extract(req)
	req = req.WithContext(tag.WithMap(req.Context(), tag.NewMap(tag.MapUpdate{
		MultiKV: tags,
	})))
	ctx, span := tracer.Start(
		req.Context(),
		"fibonacci",
		trace.WithAttributes(attrs...),
		trace.ChildOf(spanCtx),
	)
	defer span.Finish()

	var err error
	var i int
	if len(req.URL.Query()["i"]) != 1 {
		err = fmt.Errorf("Wrong number of arguments.")
	} else {
		i, err = strconv.Atoi(req.URL.Query()["i"][0])
	}
	if err != nil {
		fmt.Fprintf(w, "Couldn't parse index '%s'.", req.URL.Query()["i"])
		w.WriteHeader(503)
		// This shouldn't be necessary in a finished OTel http auto-instrument.
		span.SetStatus(codes.InvalidArgument)
		return
	}
	ret := 0

	if i < 2 {
		ret = 1
	} else {
		// Call /fib?i=(n-1) and /fib?i=(n-2) and add them together.
		var mtx sync.Mutex
		var wg sync.WaitGroup
		client := http.DefaultClient
		for offset := 1; offset < 3; offset++ {
			wg.Add(1)
			go func(n int) {
				err := tracer.WithSpan(ctx, "fibClient", func(ctx context.Context) error {
					url := fmt.Sprintf("http://localhost:3000/fib?i=%d", n)
					req, _ := http.NewRequest("GET", url, nil)
					ctx, req, inj := httptrace.W3C(ctx, req)
					trace.Inject(ctx, inj)
					res, err := client.Do(req)
					if err != nil {
						return err
					}
					body, err := ioutil.ReadAll(res.Body)
					res.Body.Close()
					if err != nil {
						return err
					}
					resp, err := strconv.Atoi(string(body))
					if err != nil {
						return err
					}
					trace.CurrentSpan(ctx).SetStatus(codes.OK)
					mtx.Lock()
					defer mtx.Unlock()
					ret += resp
					return err
				})
				if err != nil {
					fmt.Fprintf(w, "Failed to call child index '%s'.", n)
					w.WriteHeader(503)
					span.SetStatus(codes.Internal)
				}
				wg.Done()
			}(i - offset)
		}
		wg.Wait()
	}
	fmt.Fprintf(w, "%d", ret)
}

func updateDiskMetrics(ctx context.Context, used, quota metric.Float64Gauge) {
	for {
		var stat syscall.Statfs_t
		wd, _ := os.Getwd()
		syscall.Statfs(wd, &stat)

		all := float64(stat.Blocks) * float64(stat.Bsize)
		free := float64(stat.Bfree) * float64(stat.Bsize)
		used.Set(ctx, all-free)
		quota.Set(ctx, all)
		time.Sleep(time.Minute)
	}
}

func main() {
	mux := http.NewServeMux()
	mux.Handle("/", http.HandlerFunc(rootHandler))
	mux.Handle("/favicon.ico", http.NotFoundHandler())
	mux.Handle("/fib", http.HandlerFunc(fibHandler))
	mux.Handle("/quitquitquit", http.HandlerFunc(restartHandler))
	os.Stderr.WriteString("Initializing the server...\n")

	ctx := tag.NewContext(context.Background(),
		tag.Insert(appKey.String(os.Getenv("PROJECT_DOMAIN"))),
		tag.Insert(containerKey.String(os.Getenv("HOSTNAME"))),
	)

	used := meter.GetFloat64Gauge(ctx, diskUsedMetric)
	quota := meter.GetFloat64Gauge(ctx, diskQuotaMetric)

	go updateDiskMetrics(ctx, used, quota)

	err := http.ListenAndServe(":3000", mux)
	if err != nil {
		log.Fatalf("Could not start web server: %s", err)
	}
}
