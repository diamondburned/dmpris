package main

import (
	"bufio"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"

	"github.com/diamondburned/arikawa/v2/api"
	"github.com/diamondburned/arikawa/v2/discord"
	"github.com/diamondburned/arikawa/v2/gateway"
	"github.com/diamondburned/arikawa/v2/utils/httputil"
	"github.com/pkg/errors"
)

// MetadataFormat is the metadata format used for playerctl's -f flag.
const MetadataFormat = "{{ status }}: {{ artist }} - {{ title }}"

// ActivityAge is the age of a status before it is cleared.
const ActivityAge = 10 * time.Minute

func main() {
	client := api.NewClient(os.Getenv("TOKEN"))

	interrupt := make(chan os.Signal)
	signal.Notify(interrupt, os.Interrupt)

	metadataOut := make(chan string)
	c, err := startMPRISNotify(MetadataFormat, metadataOut)
	if err != nil {
		log.Fatalln("mpris error:", err)
	}

	// Gracefully stop playerctl when done using SIGINT.
	defer c.Process.Signal(os.Interrupt)

	log.Println("MPRIS playerctl started.")

	var timer *time.Timer
	var timeCh <-chan time.Time

	var metadata string

	for {
		select {
		case metadata = <-metadataOut:
			// Create the timer if we haven't already. We'll need it from here
			// on.
			if timer == nil {
				timer = time.NewTimer(0)
				<-timer.C

				timeCh = timer.C
			}

		case <-timeCh:

		case <-interrupt:
			return
		}

		var status *gateway.CustomUserStatus

		// Check if we're still playing. Reset our status otherwise.
		if parts := strings.SplitN(metadata, ": ", 2); parts[0] == "Playing" {
			status = &gateway.CustomUserStatus{
				EmojiName: "🎵",
				Text:      "Listening to " + parts[1],
				ExpiresAt: discord.NewTimestamp(time.Now().Add(ActivityAge)),
			}
		}

		// Reschedule another update.
		timer.Reset(ActivityAge)

		opt := httputil.WithJSONBody(userSettings{
			CustomStatus: status,
		})

		if err := client.FastRequest("PATCH", settingsEndpoint, opt); err != nil {
			log.Println("failed to PATCH user settings:", err)
		}
	}
}

var settingsEndpoint = api.EndpointMe + "/settings"

type userSettings struct {
	CustomStatus *gateway.CustomUserStatus `json:"custom_status"`
}

func startMPRISNotify(fmt string, out chan<- string) (*exec.Cmd, error) {
	cmd := exec.Command("playerctl", "-a", "-F", "metadata", "-f", fmt)

	o, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get playerctl stdout")
	}

	if err := cmd.Start(); err != nil {
		o.Close()
		return nil, errors.Wrap(err, "failed to start playerctl")
	}

	go func() {
		defer cmd.Process.Kill()
		defer o.Close()

		var scanner = bufio.NewScanner(o)
		for scanner.Scan() {
			out <- scanner.Text()
		}
	}()

	return cmd, nil
}
