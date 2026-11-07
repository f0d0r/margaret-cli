package metaheur

import "testing"

func TestIsProseTitle(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"truncated body", "Mindketten nehezen szedték a levegot, és egyikük sem volt hajlandó meghátrálni", true},
		{"relative continuation", "Az uszoncok voltak az elsők a csillagtörzsek közül, amelyek", true},
		{"long multi-clause", "A búgás most már élesen és tisztán zengett, és nyálkás zajok hallatszottak mögülük, hangosabbak, mint a lovaik patái alatt szé", true},
		{"function word ending", "Szép férfi volt; nagy, éjfekete szeme különösen finom és", true},
		{"article ending", "Egy külföldi utazás során sok kellemetlenség adódhat, kezdve a", true},
		{"lowercase start", "bár ember teremtette gépi ész vagy még inkább a mesterséges intelligencia - szintén általunk kezdeményezett - evolúciójának me", true},
		{"trailing colon", "A szerző eddig megjelent regényei az Agave kiadásában:", true},
		{"sentence ending", "Ez egy nagyon hosszú mondat arról, hogy mi történt a faluban azon a nyáron.", true},
		{"long english title", "A Smarter Way to Learn JavaScript: The new approach that uses technology to cut your effort in half", false},
		{"long subtitle title", "Understanding the GPS - An Introduction to the Global Positioning System - What It Is and How It Works", false},
		{"classic title", "The Curious Incident of the Dog in the Night-Time", false},
		{"hungarian title with fraction", "A 23 és 3/4 éves Adrian Mole küzdelmei", false},
		{"question title", "Ez már a kommunizmus, vagy lesz ennél még rosszabb is?", false},
		{"slash abbreviation", "TCP/IP Illustrated", false},
		{"short title", "Dune", false},
		{"list title", "Great Slow Kings, The", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsProseTitle(c.in); got != c.want {
				t.Errorf("IsProseTitle(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
