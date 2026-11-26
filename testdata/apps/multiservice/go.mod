module example.com/multiservice

go 1.27.0

require scenery.sh v0.0.0

require (
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/text v0.44.0 // indirect
)

replace scenery.sh => ../../..
