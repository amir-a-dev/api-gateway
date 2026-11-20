package main

import (
	"fmt"
	"gogateway"
	"log"
	"net/http"
)

func useInnerMiddleWareTest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("hi this is inner middle ware")
		next.ServeHTTP(w, r)
	})
}

func useOterMiddleWareTest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("hi this is outer middle ware")
		next.ServeHTTP(w, r)
	})
}

func main() {
	//initiate gateway
	gw := gogateway.New()

	//add routes
	if err := gw.AddRoute("http://localhost:8080", "/user/"); err != nil {
		log.Fatal(err)
	}

	if err := gw.AddRoute("http://localhost:8080", "/database/"); err != nil {
		log.Fatal(err)
	}

	if err := gw.UseInner(useInnerMiddleWareTest); err != nil {
		log.Fatal(err)
	}

	//add HS256 authentication
	gw.SetAuthSecret("my-secret")
	//	Add in memory token bucket rate limiter
	gw.SetRatelimiterAtribute(2, 1)

	if err := gw.Use(useOterMiddleWareTest); err != nil {
		log.Fatal(err)
	}
	//
	//run your gateway server
	if err := gw.Run(":9091"); err != nil {
		log.Fatal(err)
	}
}
