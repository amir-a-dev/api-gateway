package main

import (
	"gogateway"
	"log"
)

func main() {
	//get the gateway struct
	gw := gogateway.New()

	//add routes
	if err := gw.AddServer("http://localhost:8080", "/user/"); err != nil {
		log.Fatal(err)
	}

	if err := gw.AddServer("http://localhost:8080", "/database/"); err != nil {
		log.Fatal(err)
	}
	//add HS256 authentication
	gw.SetAuthSecret("my-secret")
	//Add in memory tokenbuket ratelimiter
	gw.SetRatelimiterAtribute(2, 1)

	//run your gateway server
	if err := gw.Run(":9091"); err != nil {
		log.Fatal(err)
	}
}
