// Command matchcheck lists the parks in a builddata report whose boundary
// name no longer counts as the same place under the current name rules.
package main

import (
	"encoding/csv"
	"fmt"
	"log"
	"os"

	"github.com/jsttylr/park-map/internal/match"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: matchcheck report.csv")
	}
	f, err := os.Open(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		log.Fatal(err)
	}
	n := 0
	for _, r := range rows[1:] {
		if r[4] != "" && !match.SharesName(r[1], r[4]) {
			n++
			fmt.Printf("%s\t%s\t%s\t%s\tparts=%s\n", r[0], r[1], r[2], r[4], r[11])
		}
	}
	fmt.Printf("%d parks would lose their boundary\n", n)
}
