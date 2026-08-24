package main

import (
	"flag"
	"fmt"
	"strings"
)

func addWaterMaterialsFlag() {
	flag.Bool("water-materials", false, "Enable advanced materials & atmospheric water harvesting research search")
}

func handleWaterMaterialsSearch() bool {
	f := flag.Lookup("water-materials")
	if f == nil || !f.Value.(flag.Getter).Get().(bool) {
		return false
	}

	fmt.Println("Starting advanced materials & atmospheric water harvesting search...")

	wmKeywords := []string{
		// Metal-Organic Frameworks
		"metal organic framework MOF", "MOF water adsorption", "MOF porous coordination polymer",
		"MOF synthesis hydrothermal solvothermal", "MOF gas storage separation",
		"MOF-based water harvesting", "zirconium MOF UiO-66", "MOF-801 water capture",

		// Covalent Organic Frameworks
		"covalent organic framework COF", "COF porous crystalline", "COF imine linkage",
		"COF water sorption", "COF photocatalysis", "COF reticular chemistry",

		// Porous Organic Polymers
		"porous organic polymer POP", "hypercrosslinked polymer HCP",
		"polymer of intrinsic microporosity PIM", "conjugated microporous polymer CMP",
		"porous aromatic framework PAF",

		// Hydrogel atmospheric water harvesting
		"hydrogel atmospheric water harvesting", "hygroscopic hydrogel sorbent",
		"superabsorbent hydrogel water capture", "polyacrylamide hydrogel water generator",
		"hydrogel solar evaporation water collection", "LiCl hydrogel moisture sorption",
		"atmospheric water generation AWG", "fog collection dew harvesting",

		// Aerogels
		"aerogel silica thermal insulation", "aerogel water adsorption",
		"cellulose aerogel biosorbent", "graphene aerogel composite",
		"carbon aerogel supercapacitor", "aerogel fabrication sol-gel",

		// Bio-inspired water collection
		"bio-inspired water collection", "Namib desert beetle fog harvesting",
		"cactus spine wettability gradient", "spider silk water capture",
		"lotus effect superhydrophobic", "pitcher plant slippery surface SLIPS",
		"bioinspired surface wettability", "fog collection biomimetic",
		"directional water transport surface", "Stenocara beetle surface texture",

		// Programmable nanomaterials
		"programmable nanomaterials stimuli-responsive", "shape memory polymer nanocomposite",
		"DNA nanotechnology programmable assembly", "colloidal self-assembly nanoparticle",
		"programmable matter reconfigurable", "active matter responsive material",
		"4D printing shape morphing", "hydrogel actuator soft robotics",

		// Bio-inspired programmable materials
		"bio-inspired programmable material", "morphing structure bioinspired",
		"kirigami origami metamaterial", "auxetic metamaterial negative Poisson",
		"soft matter responsive polymer", "liquid crystal elastomer LCE actuator",
	}

	keywords = append(keywords, wmKeywords...)
	keywords = removeDuplicates(keywords)
	seedKeywords = append(seedKeywords, wmKeywords...)
	allSeedKeywords = append(allSeedKeywords, wmKeywords...)

	seeds := []string{
		// MOF / COF / POP — primary journals & databases
		"https://www.nature.com/subjects/metal-organic-frameworks",
		"https://www.nature.com/subjects/covalent-organic-frameworks",
		"https://pubs.acs.org/journal/acsnano",
		"https://pubs.acs.org/journal/aamick",
		"https://www.sciencedirect.com/journal/microporous-and-mesoporous-materials",
		"https://www.rsc.org/journals-books-databases/find-an-journal/journal-of-materials-chemistry-a/",
		"https://arxiv.org/search/?query=metal+organic+framework&searchtype=all",
		"https://arxiv.org/search/?query=covalent+organic+framework&searchtype=all",
		"https://www.cambridge.org/core/journals/mrs-bulletin",

		// Atmospheric water harvesting
		"https://arxiv.org/search/?query=atmospheric+water+harvesting&searchtype=all",
		"https://www.sciencedirect.com/search?query=atmospheric+water+generation",
		"https://www.nature.com/search?q=atmospheric+water+harvesting",
		"https://phys.org/tags/water+harvesting/",
		"https://newscenter.lbl.gov/tag/water/",
		"https://www.sciencedaily.com/news/matter_energy/materials_science/",

		// Aerogels
		"https://phys.org/tags/aerogel/",
		"https://www.sciencedirect.com/search?query=aerogel+water+adsorption",
		"https://www.aerogel.org/",

		// Bio-inspired surfaces & materials
		"https://www.nature.com/subjects/bioinspired-materials",
		"https://www.rsc.org/journals-books-databases/find-an-journal/soft-matter/",
		"https://arxiv.org/search/?query=bioinspired+water+collection&searchtype=all",
		"https://phys.org/tags/bio-inspired/",
		"https://www.nanowerk.com/nanotechnology-news/newsid=57000.php",

		// Programmable nanomaterials
		"https://www.nature.com/subjects/programmable-materials",
		"https://arxiv.org/search/?query=programmable+nanomaterials&searchtype=all",
		"https://arxiv.org/search/?query=responsive+polymer+nanocomposite&searchtype=all",
		"https://www.sciencedirect.com/search?query=programmable+matter+soft+robotics",
		"https://www.nature.com/subjects/smart-materials",

		// General advanced materials hubs
		"https://www.materialstoday.com/",
		"https://www.nanowerk.com/",
		"https://www.mrs.org/publications/mrs-bulletin",
		"https://www.acs.org/content/acs/en/pressroom/newsreleases.html",
		"https://pubs.acs.org/journal/acsmaterialslett",
		"https://www.advancedsciencenews.com/",
		"https://onlinelibrary.wiley.com/journal/15214095",
	}

	kw := strings.Join(keywords, ",")
	for _, u := range seeds {
		addToQueue(u, 0, kw)
	}

	return true
}
