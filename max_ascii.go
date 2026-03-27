package main

import (
	"fmt"
	"math/rand"
	"time"
)

// MaxMood represents Max's emotional state.
type MaxMood int

const (
	MoodNeutral MaxMood = iota
	MoodHappy
	MoodThinking
	MoodConfused
	MoodSad
	MoodProud
	MoodSleeping
	MoodAlert
	MoodExcited
	MoodWaving
)

// MaxFrame holds one ASCII art frame with a speech line.
type MaxFrame struct {
	Art    string
	Speech string
}

// GetMaxArt returns ASCII art for the given mood with an optional speech line.
func GetMaxArt(mood MaxMood, speech string) string {
	frame := maxFrames[mood]
	if speech != "" {
		frame.Speech = speech
	}

	art := frame.Art
	if frame.Speech != "" {
		art += fmt.Sprintf("   %s", frame.Speech)
	}
	return art
}

// GetMaxGreeting returns a random greeting from Max.
func GetMaxGreeting() string {
	greetings := []string{
		"Hey! I'm Max, your QA companion.",
		"Meow! Ready to hunt some bugs?",
		"*stretches* Let's find some regressions.",
		"*perks ears up* I smell untested code.",
		"Nine lives, zero regressions. Let's go.",
	}
	return greetings[rand.Intn(len(greetings))]
}

// GetMaxThought returns a random thinking message.
func GetMaxThought() string {
	thoughts := []string{
		"*tail swishing* thinking...",
		"*ears forward* processing...",
		"*pupils dilate* analyzing...",
		"*kneading paws* working on it...",
		"*whiskers twitch* almost there...",
	}
	return thoughts[rand.Intn(len(thoughts))]
}

// GetMaxSuccess returns a random success message.
func GetMaxSuccess() string {
	successes := []string{
		"*purrs loudly* nailed it!",
		"*happy tail flick* done!",
		"*rolls over* success!",
		"*slow blink* perfect.",
		"*headbutt* great work!",
	}
	return successes[rand.Intn(len(successes))]
}

// GetMaxError returns a random error reaction.
func GetMaxError() string {
	errors := []string{
		"*hisses at the bug* something went wrong.",
		"*flattens ears* that didn't work.",
		"*knocks it off the table* error detected.",
		"*arched back* hmm, not right.",
	}
	return errors[rand.Intn(len(errors))]
}

// AnimateMax prints Max with a brief animation effect.
func AnimateMax(mood MaxMood, speech string) {
	// Clear and print with a slight delay for animation feel
	art := GetMaxArt(mood, speech)
	fmt.Println(art)
}

// AnimateMaxTransition shows Max changing from one mood to another.
func AnimateMaxTransition(from, to MaxMood, speech string) {
	fmt.Print("\033[?25l") // hide cursor
	defer fmt.Print("\033[?25h") // show cursor

	// Show "from" briefly
	fmt.Print("\r" + GetMaxArt(from, ""))
	time.Sleep(300 * time.Millisecond)

	// Clear and show "to"
	// Move up enough lines to overwrite
	lines := countLines(GetMaxArt(from, ""))
	for i := 0; i < lines; i++ {
		fmt.Print("\033[A\033[2K") // move up, clear line
	}
	fmt.Println(GetMaxArt(to, speech))
}

func countLines(s string) int {
	n := 1
	for _, c := range s {
		if c == '\n' {
			n++
		}
	}
	return n
}

// SpinnerFrames for the waiting animation.
var SpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

var maxFrames = map[MaxMood]MaxFrame{
	MoodNeutral: {
		Art: `  /\_/\
 ( o.o )
  > ^ <`,
	},

	MoodHappy: {
		Art: `  /\_/\
 ( ^.^ )
  > ~ <
 /|   |\`,
		Speech: "*purrs*",
	},

	MoodThinking: {
		Art: `  /\_/\
 ( o.O )  ?
  > ~ <
  |   |`,
		Speech: "*tail swishing*",
	},

	MoodConfused: {
		Art: `  /\_/\
 ( @.@ )  ??
  > ~ <
  /   \`,
		Speech: "*head tilt*",
	},

	MoodSad: {
		Art: `  /\_/\
 ( ;_; )
  > n <
  |   |`,
		Speech: "*sad meow*",
	},

	MoodProud: {
		Art: `  /\_/\
 ( ᵔ.ᵔ )  !
  > ^ <
 /|   |\
(_|   |_)`,
		Speech: "*stands tall*",
	},

	MoodSleeping: {
		Art: `  /\_/\
 ( -.- ) z z z
  > ~ <
  |   |`,
		Speech: "*napping*",
	},

	MoodAlert: {
		Art: `  /!\
 /\_/\
 ( O.O ) !!
  > ! <
 /|   |\`,
		Speech: "*ears up*",
	},

	MoodExcited: {
		Art: `   /\_/\
  ( >w< )  ~!
   > ^ <
  /| ~ |\
 (_|   |_)`,
		Speech: "*zoomies*",
	},

	MoodWaving: {
		Art: `  /\_/\    /
 ( o.o )  /
  > ^ < /
 /|   |`,
		Speech: "*waves paw*",
	},
}

