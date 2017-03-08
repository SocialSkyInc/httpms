package helpers

import (
	"testing"

	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func TestProjectRoot(t *testing.T) {
	// If you are running the tests you should always get the source root

	path, err := ProjectRoot()

	if err != nil {
		t.Errorf(err.Error())
	}

	p := strings.Split(os.ExpandEnv("$GOPATH"), ":")[0]
	expected := filepath.Join(p, filepath.FromSlash("src/github.com/ironsmile/httpms"))

	if path != expected {
		t.Errorf(fmt.Sprintf("Expected `%s` but got `%s`", expected, path))
	}
}

func TestAbsolutePathFunctin(t *testing.T) {
	found := AbsolutePath("file", "/root/to/")
	expected := "/root/to/file"
	if found != expected {
		t.Errorf("Expected %s but got %s", expected, found)
	}

	found = AbsolutePath("/file", "/root/to/")
	expected = "/file"
	if found != expected {
		t.Errorf("Expected %s but got %s", expected, found)
	}
}

func TestTrackNumberGuessing(t *testing.T) {
	var tracks = []struct {
		path     string
		expected int64
	}{
		// Different types of directory structure
		{`/Users/iron4o/Music/Rob Zombie/The Sinister Urge/05 Iron Head.mp3`, 5},
		{`/Users/iron4o//05 Iron Head.mp3`, 5},
		{`/home/iron4o//05 Iron Head.mp3`, 5},
		{`05 Iron Head.mp3`, 5},

		// Different types of writing the track number
		{`14. War Machine.mp3`, 14},
		{`06 - Back In Black.mp3`, 6},
		{`05 Counterstrike.mp3`, 5},
		{"05\tCounterstrike.mp3", 5},
		{`01.Ahat - Chernata ovtsa.mp3`, 1},
		{`03-in_a_gadda_da_vida_iron_butterfly_cover.mp3`, 3},
		{`8 Iron Maiden - Charlotte The Harlot.mp3`, 8},
		{`Iron Maiden - 7 - Quest For Fire.mp3`, 7},
		{`[Iron Maiden] - 06__Wasting love.mp3`, 6},
		{`METALLICA - (04) One.mp3`, 4},
		{`06)Slither.mp3`, 6},
		{`#11_12_Chelovek na Lune.mp3`, 12},
		{`#1_05_Nikto ne poverit.mp3`, 5},
		{`Nightwish-07-Ocean_Soul.mp3`, 7},
		{`nightwish -10- Beauty Of The Beast.mp3`, 10},
		{`Fatboy Slim - [14] Brimful Of Asha (Cornershop).mp3`, 14},

		// Traps which should return 0. If for some reason there is a conflict
		// between a "trap" and something from the previous category, the guess
		// should return 0. That is to say, it should be cautious and use only
		// high confidance guesses.
		{`B4 - Whole Lotta Rosie.mp3`, 0},
		{`Guns N' Roses - Estranged.mp3`, 0},
		{`Apollo 440 - Stop The Rock.mp3`, 0},
		{`Blur - Song 2.mp3`, 0},
		{`D2 - Ledeno Momiche.mp3`, 0},
		{`Factory 81- Insane in the Membrane.mp3`, 0},
		{`Five For Fighting - 100 Years.mp3`, 0},
		{`Heineken 2006 - Teddybears Sthlm & Mad Cobra - Cobrastyle.mp3`, 0},
		{`Rob Zombie - Quake 2 Theme Song.mp3`, 0},
		{`The Connells - '74-'75.mp3`, 0},
		{`TONKO-1 - Druss,druss.mp3`, 0},
		{``, 0},
		{`- 5 -`, 0},
		{`3`, 0},
		{`3.`, 0},
		{`/Users/iron4o/`, 0},
	}

	for _, test := range tracks {
		found := GuessTrackNumber(test.path)

		if found != test.expected {
			t.Errorf("Error guessing `%s`. Expected %d but got %d.", test.path,
				test.expected, found)
		}
	}
}
