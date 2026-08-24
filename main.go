// main.go - Magellan Spider: A specialized web crawler for scientific and technical research
//
// This spider crawls academic, governmental, and authoritative sources for
// specialized research topics including physics, STEAM, and more.
//
// Build with: go build -o magellan-spider.exe .
// Run with: ./magellan-spider.exe -keyword "topic" -limit 100

package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Global variables
var (
	db               *sql.DB
	queue            []string
	searched         = make(map[string]bool)
	queued           = make(map[string]bool)
	visited          = make(map[string]bool)
	keywords         []string
	seedKeywords     []string
	allSeedKeywords  []string
	mu               sync.Mutex
	robotsCache      = make(map[string][]string)
	userAgent        = "MagellanSpider/1.0 (Research Crawler; +https://github.com/magellan-spider)"
	crawlerConfig    map[string]interface{}
)

// Configuration variables
var (
	maxConcurrentWorkers = 10
	requestTimeout       = 30 * time.Second
	maxRetries           = 3
	maxRedirects         = 5
)

// noFollowDomains are social-media / video platforms whose pages we don't want
// to crawl. Links pointing to them are silently dropped from the queue.
// Images/files found at these URLs are still recorded (but upload_db.py will
// filter most of them as junk anyway).
var noFollowDomains = []string{
	// Social media
	"facebook.com", "fb.com", "fb.me", "connect.facebook.net",
	"twitter.com", "x.com", "t.co",
	"instagram.com",
	"linkedin.com",
	"tiktok.com",
	"snapchat.com",
	"pinterest.com",
	"youtube.com", "youtu.be",
	"twitch.tv",
	"discord.com", "discord.gg",
	"threads.net",
	"bsky.app",
	"wikipedia.org", "en.wikipedia.org", "simple.wikipedia.org",
	// Analytics / tracking / ad networks
	"google-analytics.com", "googletagmanager.com", "googletagservices.com",
	"doubleclick.net", "googlesyndication.com",
	"scorecardresearch.com", "omtrdc.net", "demdex.net",
	"pub.network", "a.pub.network",
	"cloudflareinsights.com",
	"hotjar.com", "clarity.ms",
	"newrelic.com", "nr-data.net",
	"segment.com", "segment.io",
	"mixpanel.com", "amplitude.com",
	// CDN / font / asset hosts (no crawlable content)
	"googleapis.com", "gstatic.com", "ajax.googleapis.com",
	"fonts.googleapis.com", "fonts.gstatic.com",
	"use.fontawesome.com", "cdnjs.cloudflare.com",
	"cdn.jsdelivr.net", "unpkg.com",
	"cloudfront.net", "fastly.net",
	"akamaized.net", "akamai.net",
}

// blockedDomains are domains we never want to crawl
var blockedDomains = []string{
	"pornhub.com", "xvideos.com", "xnxx.com", "xhamster.com", "redtube.com",
	"youporn.com", "tube8.com", "beeg.com", "spankbang.com", "4tube.com",
	"onlyfans.com", "chaturbate.com", "livejasmin.com", "cam4.com", "stripchat.com",
	"thepiratebay.org", "1337x.to", "rarbg.to", "kickasstorrents.to",
	"torrentz2.eu", "nyaa.si", "rutracker.org", "limetorrents.info",
	"crackwatch.com", "warezbb.org", "0day.xyz",
}

// blockedPathKeywords are path segments that indicate we should skip a URL
var blockedPathKeywords = []string{
	"/porn", "/xxx", "/adult-", "/sex/", "/nude", "/nsfw",
	"/hentai", "/escort", "/cam-girls",
	"/torrent", "/crack/", "/warez", "/keygen", "/pirated",
	// Static assets — crawling these wastes limit slots
	"/_next/static/", "/static/css/", "/static/js/", "/static/media/",
	"/assets/css/", "/assets/js/", "/assets/fonts/",
	".woff", ".woff2", ".ttf", ".eot", ".otf",
	".min.js", ".min.css", ".bundle.js", ".chunk.js",
	"/cdn-cgi/", "/__webpack",
}

func main() {
	// Define all flags before flag.Parse()
	keyword := flag.String("keyword", "", "Search keyword")
	limit := flag.Int("limit", 200, "Maximum number of pages to crawl per keyword")
	dbPath := flag.String("db", "magellan.sq3", "Path to SQLite database")
	seedFile := flag.String("seeds", "", "JSON file containing seed URLs")
	configFile := flag.String("config", "", "Configuration file for crawler settings")
	verbose := flag.Bool("verbose", false, "Enable verbose output")
	addQuantumBiologyFlag()
	addInfectionResearchFlag()
	addNewsFlag()
	addWaterMaterialsFlag()
	flag.Parse()

	// Load configuration if provided
	crawlerConfig = make(map[string]interface{})
	if *configFile != "" {
		var err error
		crawlerConfig, err = loadConfig(*configFile)
		if err != nil {
			fmt.Printf("Warning: Could not load config file: %v\n", err)
		}
	}

	// Initialize database
	initDB(*dbPath)
	enhanceDatabaseSchema()
	defer db.Close()

	// Load infection research configuration
	loadInfectionConfig()

	// Handle specialized searches
	newsHandled := handleNewsSearch()
	quantumBioHandled := handleQuantumBiologySearch()
	infectionHandled := handleInfectionResearchSearch()
	waterMaterialsHandled := handleWaterMaterialsSearch()

	// If no specialized search was handled, use the keyword approach
	if !newsHandled && !quantumBioHandled && !infectionHandled && !waterMaterialsHandled {
		if *keyword == "" {
			fmt.Println("Error: No keyword provided and no specialized search selected")
			flag.Usage()
			os.Exit(1)
		}
		keywords = []string{*keyword}

		// Seed with crawlable, server-rendered sites organized by topic category.
		kwEncoded := url.QueryEscape(*keyword)
		kwLower := strings.ToLower(*keyword)

		// Base seeds — work for any topic
		seeds := []string{
			"https://theconversation.com/search?q=" + kwEncoded,
			"https://www.sciencedaily.com/search/?keyword=" + kwEncoded,
			"https://www.thoughtco.com/search?q=" + kwEncoded,
			"https://www.livescience.com/search?searchTerm=" + kwEncoded,
			"https://worldhistory.org/search/?q=" + kwEncoded,
			"https://ocw.mit.edu/search/?q=" + kwEncoded,
			"https://openstax.org/subjects",
		}

		// Programming / computer science
		containsAny := func(s string, terms []string) bool {
			for _, t := range terms {
				if strings.Contains(s, t) {
					return true
				}
			}
			return false
		}
		if containsAny(kwLower, []string{"program", "code", "software", "language", "python", "javascript",
			"rust", "golang", " go ", "java", "c++", "linux", "database", "compiler",
			"algorithm", "data structure", "web dev", "open source", "computing"}) {
			seeds = append(seeds,
				"https://docs.python.org/3/tutorial/",
				"https://docs.python.org/3/library/",
				"https://developer.mozilla.org/en-US/docs/Learn",
				"https://developer.mozilla.org/en-US/docs/Web/JavaScript/Guide",
				"https://go.dev/doc/effective_go",
				"https://www.rust-lang.org/learn",
				"https://learnxinyminutes.com/",
				"https://realpython.com/tutorials/",
				"https://www.geeksforgeeks.org/"+kwEncoded+"/",
				"https://exercism.org/tracks",
				"https://www.gnu.org/software/",
				"https://www.freecodecamp.org/news/search/?query="+kwEncoded,
			)
		}

		// History / archaeology / civilizations
		if containsAny(kwLower, []string{"history", "ancient", "medieval", "revolution", "war",
			"empire", "civilization", "dynasty", "colonial", "byzantine", "roman",
			"greek", "egypt", "mesopotamia", "industrial", "cold war"}) {
			seeds = append(seeds,
				"https://www.historyhit.com/",
				"https://www.bbc.co.uk/history/",
				"https://ehistory.osu.edu/",
				"https://www.gilderlehrman.org/history-resources",
				"https://www.smithsonianmag.com/category/history-archaeology/",
				"https://www.smithsonianmag.com/smart-news/",
			)
		}

		// Science — physics, chemistry, biology, astronomy
		if containsAny(kwLower, []string{"physics", "chemistry", "biology", "astronom", "cosmolog",
			"quantum", "genetic", "neuroscience", "climate", "ecology", "evolution",
			"paleontol", "geology", "oceanograph", "thermodynamic", "nuclear"}) {
			seeds = append(seeds,
				"https://www.quantamagazine.org/physics/",
				"https://www.quantamagazine.org/biology/",
				"https://phys.org/physics-news/",
				"https://www.sciencenews.org/",
				"https://www.pbs.org/newshour/science",
			)
		}

		// Arts / music / culture
		if containsAny(kwLower, []string{"art", "music", "paint", "sculpt", "architect", "film",
			"literature", "poetry", "theater", "dance", "renaissance", "baroque",
			"classical", "jazz", "folk", "culture", "craft"}) {
			seeds = append(seeds,
				"https://www.smithsonianmag.com/category/arts-culture/",
				"https://www.metmuseum.org/learn",
				"https://www.khanacademy.org/humanities",
				"https://www.musictheory.net/",
			)
		}

		// Hobbies — railroads, woodworking, photography, gardening, chess, etc.
		if containsAny(kwLower, []string{"railroad", "train", "railway", "woodwork", "photograph",
			"chess", "garden", "cook", "hiking", "astronomy amateur", "origami",
			"model", "stamp", "coin", "aquarium", "beekeep", "3d print", "radio",
			"birdwatch", "knit", "sew", "brew"}) {
			seeds = append(seeds,
				"https://www.railwaymuseum.org.uk/stories",
				"https://www.gardeningknowhow.com/",
				"https://www.bhg.com/gardening/",
				"https://www.woodworking.com/",
				"https://www.bhg.com/crafts/",
				"https://www.chess.com/learn-how-to-play-chess",
				"https://lichess.org/learn",
				"https://www.dpreview.com/learn",
				"https://petapixel.com/",
			)
		}

		// Economics / social sciences / philosophy
		if containsAny(kwLower, []string{"economic", "finance", "psycholog", "sociolog", "anthropolog",
			"philosoph", "ethic", "politic", "legal", "law", "linguistic"}) {
			seeds = append(seeds,
				"https://plato.stanford.edu/contents.html",
				"https://www.econlib.org/",
				"https://www.simplypsychology.org/",
				"https://www.khanacademy.org/economics-finance-domain",
			)
		}

		// Statistics / probability / mathematics
		if containsAny(kwLower, []string{"statistic", "probabilit", "bayesian", "combinatoric",
			"regression", "stochastic", "monte carlo", "random variable", "hypothesis",
			"distribution", "variance", "expected value", "markov", "linear algebra",
			"calculus", "number theory", "graph theory", "set theory", "logic"}) {
			seeds = append(seeds,
				"https://mathworld.wolfram.com/",
				"https://www.mathsisfun.com/data/",
				"https://stattrek.com/",
				"https://www.stat.yale.edu/Courses/1997-98/101/",
				"https://www.khanacademy.org/math/statistics-probability",
				"https://www.khanacademy.org/math/probability",
				"https://plus.maths.org/content/",
				"https://seeing-theory.brown.edu/",
				"https://stats.libretexts.org/",
			)
		}

		// Poker / casino games / gambling mathematics (factual/mathematical only)
		if containsAny(kwLower, []string{"poker", "casino", "blackjack", "roulette", "craps",
			"baccarat", "slot", "gambling", "card game", "odds", "house edge",
			"card counting", "game theory gambling"}) {
			seeds = append(seeds,
				"https://wizardofodds.com/games/",
				"https://wizardofodds.com/gambling/math/",
				"https://wizardofodds.com/games/blackjack/basics/",
				"https://wizardofodds.com/games/poker/",
				"https://wizardofodds.com/games/roulette/basics/",
				"https://wizardofodds.com/games/craps/basics/",
				"https://www.probabilitytheory.info/",
				"https://www.cardschat.com/poker/strategy/math/",
				"https://mathworld.wolfram.com/Poker.html",
				"https://mathworld.wolfram.com/Casino.html",
			)
		}

		// Medicine / health / anatomy / nutrition
		if containsAny(kwLower, []string{"medicine", "medical", "anatomy", "physiology", "nutrition",
			"disease", "symptom", "treatment", "drug", "pharmacol", "surgery",
			"cardiology", "neurology", "oncology", "pediatric", "immunology",
			"endocrinology", "orthopedic", "dermatology", "psychiatry", "pathology",
			"epidemiology", "public health", "vaccine", "clinical", "diagnosis"}) {
			seeds = append(seeds,
				"https://www.ncbi.nlm.nih.gov/books/browse/",
				"https://medlineplus.gov/healthtopics.html",
				"https://www.mayoclinic.org/diseases-conditions",
				"https://www.khanacademy.org/science/health-and-medicine",
				"https://courses.lumenlearning.com/boundless-ap/",
				"https://openstax.org/subjects/science",
				"https://www.merckmanuals.com/home",
				"https://www.healthline.com/health",
				"https://emedicine.medscape.com/",
				"https://www.nature.com/nm/",
			)
		}

		// Engineering — civil, mechanical, electrical, aerospace, chemical
		if containsAny(kwLower, []string{"engineering", "mechanical", "civil engineer", "electrical engineer",
			"aerospace", "structural", "thermodynamic", "fluid dynamic", "material science",
			"circuit", "signal processing", "control system", "robotics", "manufacturing",
			"chemical engineer", "process engineer", "bridge", "dam", "turbine",
			"hydraulic", "pneumatic", "statics", "dynamics", "stress", "strain"}) {
			seeds = append(seeds,
				"https://www.engineeringtoolbox.com/",
				"https://ocw.mit.edu/courses/mechanical-engineering/",
				"https://ocw.mit.edu/courses/electrical-engineering-and-computer-science/",
				"https://ocw.mit.edu/courses/civil-and-environmental-engineering/",
				"https://www.efunda.com/",
				"https://nptel.ac.in/course.html",
				"https://www.khanacademy.org/science/physics",
				"https://www.engineersedge.com/",
				"https://www.engineeringclicks.com/",
				"https://www.engineeringtoolbox.com/",
				"https://www.sciencedirect.com/topics/engineering",
				"https://www.asme.org/topics-resources/content",
				"https://www.asce.org/topics/",
				"https://www.ieee.org/education/index.html",
			)
		}

		// Aviation — aircraft design, avionics, jet propulsion, flight mechanics, ATC
		if containsAny(kwLower, []string{"aviation", "aircraft", "avionics", "jet propulsion", "turbofan",
			"turbojet", "turboprop", "helicopter rotor", "flight mechanic", "flight dynamics",
			"aerodynamics lift drag", "airfoil", "wing design", "fuselage", "empennage",
			"air traffic control", "atc", "instrument flight", "ifr", "vfr",
			"commercial aviation", "airline history", "airport", "runway",
			"fly by wire", "autopilot", "inertial navigation", "gps aviation",
			"aircraft engine", "piston engine aircraft", "propeller aircraft",
			"supersonic flight", "hypersonic", "glider aerodynamics", "sailplane",
			"helicopter aerodynamics", "rotorcraft", "flight simulator",
			"wright brothers", "concorde", "boeing history", "airbus history"}) {
			seeds = append(seeds,
				"https://airandspace.si.edu/stories/editorial/",
				"https://www.faa.gov/education/",
				"https://www.faa.gov/air_traffic/publications/",
				"https://www.faa.gov/regulations_policies/handbooks_manuals/aviation/",
				"https://www.aopa.org/training-and-safety/online-learning/",
				"https://www.airspacemag.com/",
				"https://www.nasa.gov/aeronautics/",
				"https://history.nasa.gov/",
				"https://www.centennialofflight.net/",
				"https://www.aviationweek.com/",
				"https://skybrary.aero/articles/",
				"https://www.boldmethod.com/learn-to-fly/",
				"https://www.britannica.com/technology/aerospace-industry",
				"https://www.engineeringtoolbox.com/",
				"https://www.grc.nasa.gov/www/k-12/airplane/",
				"https://www.grc.nasa.gov/www/BGH/bgha.html",
				"https://www.militaryfactory.com/aircraft/",
				"https://www.airpowerworld.info/",
			)
		}

		// Chemical engineering — process design, heat/mass transfer, reaction engineering
		if containsAny(kwLower, []string{"chemical process", "distillation", "heat exchanger", "mass transfer",
			"reaction engineering", "reactor design", "chemical reactor", "unit operation",
			"separation process", "absorption column", "packed column", "plate column",
			"crystallization", "evaporation process", "drying engineering",
			"fluid flow chemical", "bernoulli chemical", "reynolds number",
			"chemical plant", "petrochemical", "refinery", "cracking", "polymerization",
			"process control", "pid controller", "chemical thermodynamics"}) {
			seeds = append(seeds,
				"https://www.engineeringtoolbox.com/",
				"https://nptel.ac.in/courses/103/101/103101001/",
				"https://ocw.mit.edu/courses/chemical-engineering/",
				"https://www.aiche.org/resources/",
				"https://www.cheresources.com/",
				"https://www.che.com/",
				"https://www.chemengonline.com/",
				"https://www.thermexcel.com/english/",
				"https://www.processdesign.mccormick.northwestern.edu/",
			)
		}

		// Nuclear engineering — reactor physics, fuel cycle, radiation
		if containsAny(kwLower, []string{"nuclear reactor", "reactor physics", "nuclear fuel", "fission",
			"fusion reactor", "nuclear power plant", "reactor design", "neutron",
			"radioactive", "radiation shielding", "nuclear waste", "enrichment",
			"pressurized water reactor", "boiling water reactor", "fast reactor",
			"nuclear safety", "chernobyl", "fukushima", "three mile island",
			"tokamak", "iter fusion", "plasma physics", "nuclear engineering"}) {
			seeds = append(seeds,
				"https://www.nrc.gov/reading-rm/basic-ref/students.html",
				"https://www.nuclear.org/",
				"https://www.world-nuclear.org/information-library.aspx",
				"https://www.iaea.org/topics/nuclear-power",
				"https://ocw.mit.edu/courses/nuclear-science-and-engineering/",
				"https://www.ans.org/nuclear/",
				"https://www.energy.gov/ne/nuclear-reactor-technologies",
				"https://www.euronuclear.org/info/encyclopaedia.htm",
				"https://www.atomicarchive.com/",
			)
		}

		// Marine / naval engineering — ship design, naval architecture, propulsion
		if containsAny(kwLower, []string{"naval architecture", "ship design", "marine engineer",
			"hull design", "buoyancy", "displacement vessel", "stability ship",
			"propulsion marine", "ship propeller", "marine diesel", "steam turbine ship",
			"submarine design", "shipbuilding", "offshore platform", "maritime",
			"naval vessel", "warship design", "cargo ship", "container ship",
			"hydrodynamics ship", "wave resistance", "ship resistance"}) {
			seeds = append(seeds,
				"https://www.maritimehandbook.com/",
				"https://www.sname.org/publications",
				"https://www.rina.org.uk/",
				"https://maritime.org/",
				"https://www.britannica.com/technology/ship",
				"https://ocw.mit.edu/courses/2-20-marine-hydrodynamics-13-021-spring-2005/",
				"https://www.marineinsight.com/",
				"https://www.the-naval-architect.com/",
				"https://navweaps.com/",
			)
		}

		// Biomedical engineering — medical devices, biomechanics, prosthetics, imaging
		if containsAny(kwLower, []string{"biomedical engineer", "medical device", "biomechanics",
			"prosthetic", "orthotic", "implant", "pacemaker", "stent",
			"medical imaging", "mri engineering", "ct scanner", "ultrasound engineering",
			"tissue engineering", "scaffold", "bioreactor", "drug delivery",
			"neural interface", "brain computer interface", "cochlear implant",
			"artificial organ", "biomaterial", "biocompatibility"}) {
			seeds = append(seeds,
				"https://ocw.mit.edu/courses/health-sciences-and-technology/",
				"https://www.bmes.org/learn",
				"https://www.nibib.nih.gov/science-education",
				"https://www.accessscience.com/content/biomedical-engineering",
				"https://www.khanacademy.org/science/health-and-medicine",
				"https://www.whitaker.org/resources",
				"https://www.nature.com/subjects/biomedical-engineering",
				"https://www.fda.gov/medical-devices/overview-device-regulation/",
			)
		}

		// Structural engineering — building design, load analysis, failure
		if containsAny(kwLower, []string{"structural engineer", "structural analysis", "load analysis",
			"beam column", "truss structure", "frame structure", "shear force",
			"bending moment", "deflection beam", "buckling", "fatigue failure",
			"fracture mechanics", "finite element analysis", "fea", "seismic design",
			"earthquake resistant", "wind load", "building code", "concrete design",
			"reinforced concrete", "steel design", "timber structure",
			"prestressed concrete", "foundation design", "retaining wall"}) {
			seeds = append(seeds,
				"https://www.engineeringtoolbox.com/",
				"https://ocw.mit.edu/courses/civil-and-environmental-engineering/",
				"https://www.asce.org/topics/",
				"https://www.structural-engineering.org/",
				"https://structuremag.org/",
				"https://www.steelconstruction.info/",
				"https://www.cement.org/learn/",
				"https://www.awc.org/education",
				"https://www.aisc.org/education/",
				"https://www.iccsafe.org/building-safety/",
			)
		}

		// Geotechnical / transportation engineering — soil, roads, tunnels
		if containsAny(kwLower, []string{"geotechnical", "soil mechanics", "foundation engineering",
			"pile foundation", "soil bearing capacity", "slope stability", "embankment",
			"retaining wall", "tunnel engineering", "underground construction",
			"transportation engineering", "highway design", "pavement design",
			"traffic engineering", "road design", "bridge engineering",
			"railway engineering track", "port harbor design", "dam engineering"}) {
			seeds = append(seeds,
				"https://www.engineeringtoolbox.com/",
				"https://ocw.mit.edu/courses/civil-and-environmental-engineering/",
				"https://www.asce.org/topics/",
				"https://www.fhwa.dot.gov/engineering/",
				"https://www.railway-technical.com/",
				"https://www.trb.org/Publications/",
				"https://onlinepubs.trb.org/",
				"https://www.icevirtuallibrary.com/",
			)
		}

		// Electrical / electronics engineering deep — power, semiconductors, RF
		if containsAny(kwLower, []string{"power system", "power grid", "transformer", "generator electric",
			"electric motor", "induction motor", "synchronous machine", "power electronics",
			"inverter", "rectifier", "semiconductor", "transistor", "mosfet", "bjt",
			"integrated circuit", "vlsi", "fpga", "microcontroller", "embedded system",
			"rf engineering", "antenna", "microwave", "radar", "telecommunications",
			"fiber optic", "photonics", "laser", "led", "optoelectronics",
			"digital logic", "boolean algebra", "logic gate", "flip flop"}) {
			seeds = append(seeds,
				"https://www.ieee.org/education/index.html",
				"https://ocw.mit.edu/courses/electrical-engineering-and-computer-science/",
				"https://www.allaboutcircuits.com/",
				"https://www.electronics-tutorials.ws/",
				"https://www.circuitdigest.com/",
				"https://learn.sparkfun.com/",
				"https://www.analog.com/en/education/education-library.html",
				"https://www.ti.com/lit/an/",
				"https://www.eevblog.com/",
				"https://www.rfcafe.com/",
			)
		}

		// Geography / cartography / geology / earth science
		if containsAny(kwLower, []string{"geography", "cartograph", "geology", "geomorpholog",
			"tectonic", "volcano", "earthquake", "erosion", "continent", "ocean",
			"river", "mountain", "climate zone", "biome", "topograph", "map",
			"geodesy", "gis", "remote sensing", "mineral", "rock", "fossil",
			"stratigraphy", "sediment", "glacial", "hydrology", "watershed"}) {
			seeds = append(seeds,
				"https://www.usgs.gov/science-topics",
				"https://oceanservice.noaa.gov/facts/",
				"https://www.nationalgeographic.org/encyclopedia/",
				"https://www.britannica.com/place",
				"https://geology.com/",
				"https://www.earthobservatory.nasa.gov/",
				"https://www.khanacademy.org/science/cosmology-and-astronomy",
				"https://www.worldatlas.com/",
				"https://www.bgs.ac.uk/geological-topics/",
			)
		}

		// Language / linguistics / etymology / writing systems
		if containsAny(kwLower, []string{"linguistic", "language", "etymology", "grammar",
			"syntax", "phonetics", "phonology", "morphology", "semantics", "pragmatic",
			"writing system", "alphabet", "script", "dialect", "pidgin", "creole",
			"endangered language", "historical linguistics", "indo-european", "proto-",
			"translation", "lexicography", "sociolinguistics", "psycholinguistics"}) {
			seeds = append(seeds,
				"https://www.etymonline.com/",
				"https://omniglot.com/writing/",
				"https://glottolog.org/",
				"https://www.linguisticsociety.org/resource/language-documentation",
				"https://plato.stanford.edu/entries/linguistics/",
				"https://www.thegreatcourses.com/search#q=linguistics",
				"https://linguistics.stackexchange.com/questions?sort=votes",
				"https://www.sil.org/resources/publications/",
				"https://www.unicode.org/faq/",
			)
		}

		// Religion / mythology / theology / sacred texts
		if containsAny(kwLower, []string{"religion", "mythology", "theology", "sacred", "scripture",
			"bible", "quran", "torah", "hinduism", "buddhism", "islam", "christianity",
			"judaism", "greek myth", "norse myth", "roman myth", "egyptian myth",
			"paganism", "shamanism", "ritual", "deity", "pantheon", "cosmogony",
			"afterlife", "creation myth", "hero myth", "taoism", "confucianism",
			"shinto", "zoroastrianism", "gnosticism", "mysticism"}) {
			seeds = append(seeds,
				"https://www.sacred-texts.com/",
				"https://www.theoi.com/",
				"https://www.ancient.eu/religion/",
				"https://www.britannica.com/topic/religion",
				"https://plato.stanford.edu/entries/religion/",
				"https://www.patheos.com/library/",
				"https://www.worldhistory.org/religion/",
				"https://www.religionfacts.com/",
				"https://www.sacred-texts.com/cla/",
			)
		}

		// Law / legal systems / jurisprudence / constitutional history
		if containsAny(kwLower, []string{"law", "legal", "jurisprudence", "constitution", "statute",
			"common law", "civil law", "contract law", "criminal law", "tort",
			"supreme court", "case law", "precedent", "international law",
			"human rights law", "property law", "intellectual property", "patent",
			"copyright", "trademark", "antitrust", "administrative law"}) {
			seeds = append(seeds,
				"https://www.law.cornell.edu/wex",
				"https://www.justia.com/",
				"https://plato.stanford.edu/entries/law-and-language/",
				"https://ocw.mit.edu/courses/sloan-school-of-management/",
				"https://legal.thomsonreuters.com/en/insights/",
				"https://www.uscourts.gov/about-federal-courts/educational-resources",
				"https://www.oyez.org/",
				"https://www.law.com/topics/",
			)
		}

		// Sports / athletics / fitness / exercise science
		if containsAny(kwLower, []string{"sport", "athletic", "fitness", "exercise", "training",
			"football", "soccer", "basketball", "baseball", "tennis", "golf",
			"swimming", "track and field", "olympics", "martial art", "boxing",
			"cycling", "running", "weightlifting", "gymnastics", "biomechanics",
			"physiology exercise", "nutrition sport", "coaching", "strategy sport"}) {
			seeds = append(seeds,
				"https://www.topendsports.com/",
				"https://www.britannica.com/sports",
				"https://www.olympic.org/sports",
				"https://www.ncbi.nlm.nih.gov/pmc/?term=exercise+physiology",
				"https://www.scienceforsport.com/",
				"https://www.humankinetics.com/",
				"https://www.runnersworld.com/training/",
				"https://breakingmuscle.com/",
			)
		}

		// Food / culinary arts / cuisine / gastronomy
		if containsAny(kwLower, []string{"food", "cuisine", "culinary", "cooking", "recipe",
			"gastronomy", "nutrition", "baking", "fermentation", "spice",
			"italian food", "french cuisine", "japanese food", "chinese food",
			"indian food", "mexican food", "food history", "food science",
			"ferment", "cheese", "bread", "pastry", "charcuterie", "wine",
			"beer brewing", "distilling", "sushi", "ramen", "tapas"}) {
			seeds = append(seeds,
				"https://www.seriouseats.com/",
				"https://www.britannica.com/topic/food",
				"https://www.worldhistory.org/article/606/food-in-the-ancient-world/",
				"https://www.sciencefriday.com/topics/food-science/",
				"https://www.foodandwine.com/",
				"https://www.nytimes.com/section/food",
				"https://www.atlasobscura.com/foods",
				"https://www.thespruceeats.com/",
				"https://www.bonappetit.com/",
			)
		}

		// Zoology / wildlife / marine biology / animal behavior
		if containsAny(kwLower, []string{"zoology", "wildlife", "animal", "mammal", "bird", "reptile",
			"amphibian", "fish", "insect", "marine biology", "cetacean", "primate",
			"predator", "prey", "migration", "hibernation", "adaptation",
			"evolution animal", "ethology", "behavioral ecology", "conservation",
			"endangered species", "taxonomy", "invertebrate", "coral reef",
			"deep sea", "ocean life", "entomology", "ornithology", "herpetology"}) {
			seeds = append(seeds,
				"https://animaldiversity.org/",
				"https://www.iucnredlist.org/",
				"https://www.nationalgeographic.com/animals/",
				"https://www.fishbase.se/",
				"https://www.marinespecies.org/",
				"https://www.audubon.org/bird-guide",
				"https://www.inaturalist.org/",
				"https://www.arkive.org/",
				"https://www.birdsoftheworld.org/",
				"https://ocean.si.edu/ocean-life",
			)
		}

		// Aviation / aerospace history / aircraft
		if containsAny(kwLower, []string{"aviation", "aircraft", "airplane", "pilot", "aeronautic",
			"aerospace history", "wright brothers", "jet engine", "propeller",
			"helicopter", "glider", "airship", "balloon", "supersonic", "concorde",
			"air force", "fighter jet", "bomber", "airship", "flight history",
			"aerodynamics", "wind tunnel", "nasa history", "rocket", "spacecraft"}) {
			seeds = append(seeds,
				"https://airandspace.si.edu/stories/editorial/",
				"https://www.faa.gov/education/",
				"https://www.aopa.org/training-and-safety/online-learning/",
				"https://www.airspacemag.com/",
				"https://www.nasa.gov/aeronautics/",
				"https://www.centennialofflight.net/",
				"https://www.britannica.com/technology/aerospace-industry",
				"https://history.nasa.gov/",
			)
		}

		// Automotive / vehicles / motorsport / mechanical engineering
		if containsAny(kwLower, []string{"automobile", "automotive", "car", "vehicle", "engine",
			"motor", "transmission", "suspension", "brake", "racing", "formula 1",
			"motorsport", "electric vehicle", "hybrid", "combustion", "drivetrain",
			"chassis", "aerodynamic car", "car history", "classic car", "truck",
			"motorcycle", "bicycle engineering", "internal combustion"}) {
			seeds = append(seeds,
				"https://www.caranddriver.com/research/",
				"https://auto.howstuffworks.com/",
				"https://www.motortrend.com/features/",
				"https://www.britannica.com/technology/automobile",
				"https://www.sae.org/learn/",
				"https://www.formula1.com/en/latest/all.html",
				"https://www.driversnote.com/",
				"https://www.racecar-engineering.com/",
			)
		}

		// Environmental science / sustainability / ecology
		if containsAny(kwLower, []string{"environment", "sustainability", "ecology", "ecosystem",
			"biodiversity", "conservation", "climate change", "global warming",
			"carbon cycle", "nitrogen cycle", "deforestation", "pollution",
			"renewable energy", "solar energy", "wind energy", "habitat",
			"food web", "nutrient cycle", "population ecology", "community ecology",
			"wetland", "rainforest", "desert ecology", "tundra", "savanna"}) {
			seeds = append(seeds,
				"https://www.epa.gov/learn-issues",
				"https://www.nature.org/en-us/what-we-do/our-insights/",
				"https://www.iucn.org/resources/",
				"https://www.khanacademy.org/science/ap-biology/ecology-ap",
				"https://www.nationalgeographic.org/topics/environment/",
				"https://climate.nasa.gov/",
				"https://www.unep.org/explore-topics",
				"https://www.worldwildlife.org/habitats",
				"https://www.nrdc.org/stories",
			)
		}

		// Military history / warfare / tactics / weapons
		if containsAny(kwLower, []string{"military history", "warfare", "battle", "siege", "strategy war",
			"tactics", "weapon", "sword", "cavalry", "navy", "world war",
			"ancient warfare", "medieval warfare", "napoleon", "artillery",
			"submarine", "tank", "infantry", "logistics military", "fortification",
			"military technology", "arms race", "nuclear weapon", "cold war military"}) {
			seeds = append(seeds,
				"https://www.militaryhistorymatters.com/",
				"https://www.history.com/topics/wars",
				"https://www.britannica.com/topic/war",
				"https://warfarehistorynetwork.com/",
				"https://www.thehistorynet.com/",
				"https://www.awm.gov.au/collection/",
				"https://www.iwm.org.uk/history/",
				"https://armyhistory.org/",
			)
		}

		// Meteorology / weather / atmospheric science
		if containsAny(kwLower, []string{"meteorology", "weather", "atmosphere", "climate",
			"hurricane", "tornado", "thunderstorm", "precipitation", "humidity",
			"pressure", "wind pattern", "jet stream", "front", "cloud",
			"lightning", "fog", "snow", "hail", "drought", "flood",
			"el nino", "monsoon", "blizzard", "cyclone", "typhoon"}) {
			seeds = append(seeds,
				"https://www.noaa.gov/education/resource-collections",
				"https://www.weather.gov/education/",
				"https://www.metoffice.gov.uk/weather/learn-about/",
				"https://scijinks.gov/menu/",
				"https://www.britannica.com/science/meteorology",
				"https://www.ametsoc.org/ams/index.cfm/education-careers/",
				"https://earth.nullschool.net/",
				"https://www.wmo.int/pages/themes/",
			)
		}

		// Botany / plants / horticulture / plant biology
		if containsAny(kwLower, []string{"botany", "plant", "flower", "tree", "shrub", "fungi",
			"photosynthesis", "pollination", "seed", "germination", "root",
			"leaf", "stem", "chlorophyll", "plant taxonomy", "herbarium",
			"horticulture", "agriculture", "crop", "grain", "fruit",
			"tropical plant", "succulent", "fern", "moss", "lichen",
			"plant ecology", "ethnobotany", "medicinal plant"}) {
			seeds = append(seeds,
				"https://www.kew.org/science/",
				"https://plants.usda.gov/home",
				"https://www.mobot.org/",
				"https://www.britannica.com/plant/plant",
				"https://botany.org/resources/",
				"https://www.inaturalist.org/taxa/47126-Plantae",
				"https://pfaf.org/",
				"https://www.ars-grin.gov/",
				"https://www.rhs.org.uk/science/",
			)
		}

		// Architecture / urban planning / built environment
		if containsAny(kwLower, []string{"architecture", "urban", "building", "design", "construction",
			"structure", "skyscraper", "dome", "arch", "gothic", "baroque architecture",
			"modernist", "brutalism", "art deco", "city planning", "zoning",
			"housing", "cathedral", "temple", "mosque", "palace", "pyramid",
			"landscape architecture", "interior design", "vernacular architecture"}) {
			seeds = append(seeds,
				"https://www.archdaily.com/",
				"https://www.architectural-review.com/",
				"https://www.khanacademy.org/humanities/ap-art-history",
				"https://www.greatbuildings.com/",
				"https://www.archinform.net/",
				"https://www.aia.org/resources/",
				"https://archnet.org/",
				"https://www.designingbuildings.co.uk/",
			)
		}

		// Psychology / cognitive science / neuroscience
		if containsAny(kwLower, []string{"psychology", "cognitive", "neuroscience", "behavior",
			"mental", "consciousness", "perception", "memory", "learning psychology",
			"motivation", "emotion", "personality", "social psychology",
			"developmental psychology", "abnormal psychology", "therapy",
			"freud", "jung", "cognitive bias", "decision making", "attention",
			"intelligence", "brain", "neuron", "synapse", "cortex"}) {
			seeds = append(seeds,
				"https://www.simplypsychology.org/",
				"https://www.apa.org/topics/",
				"https://www.psychologytoday.com/us/basics",
				"https://www.verywellmind.com/",
				"https://noba.to/",
				"https://www.khanacademy.org/science/mcat/behavior",
				"https://neuroscience.uth.edu/",
				"https://plato.stanford.edu/entries/consciousness/",
				"https://www.brainpickings.org/",
			)
		}

		// Pure mathematics — topology, abstract algebra, number theory, analysis
		if containsAny(kwLower, []string{"topology", "abstract algebra", "number theory", "real analysis",
			"complex analysis", "differential geometry", "algebraic geometry",
			"group theory", "ring theory", "field theory", "category theory",
			"functional analysis", "measure theory", "fourier analysis",
			"prime number", "modular arithmetic", "diophantine", "manifold",
			"metric space", "hilbert space", "banach space", "riemann"}) {
			seeds = append(seeds,
				"https://mathworld.wolfram.com/",
				"https://www.math.uchicago.edu/~may/",
				"https://mathoverflow.net/questions?sort=votes",
				"https://www.cut-the-knot.org/",
				"https://www.3blue1brown.com/",
				"https://brilliant.org/courses/",
				"https://math.stackexchange.com/questions?sort=votes",
				"https://www.ams.org/publicoutreach",
				"https://ncatlab.org/nlab/show/HomePage",
				"https://proofwiki.org/wiki/Main_Page",
			)
		}

		// Space science / astronomy / astrophysics / cosmology
		if containsAny(kwLower, []string{"space", "astrono", "astrophysic", "cosmolog", "galaxy",
			"black hole", "neutron star", "supernova", "nebula", "planet",
			"solar system", "exoplanet", "comet", "asteroid", "telescope",
			"hubble", "james webb", "nasa", "spacex", "moon", "mars",
			"dark matter", "dark energy", "big bang", "redshift", "quasar",
			"pulsar", "white dwarf", "stellar evolution"}) {
			seeds = append(seeds,
				"https://www.nasa.gov/topics/universe/index.html",
				"https://hubblesite.org/contents/",
				"https://www.esa.int/Science_Exploration/",
				"https://www.quantamagazine.org/physics/",
				"https://www.skyandtelescope.org/astronomy-resources/",
				"https://www.space.com/",
				"https://www.caltech.edu/research/topics/astronomy-astrophysics",
				"https://www.atnf.csiro.au/outreach/education/",
				"https://imagine.gsfc.nasa.gov/",
				"https://www.universetoday.com/",
			)
		}

		// Film / cinema history / television
		if containsAny(kwLower, []string{"film", "cinema", "movie", "director", "actor",
			"screenwriting", "cinematography", "editing", "film history",
			"silent film", "hollywood", "new wave cinema", "italian cinema",
			"french new wave", "animation", "documentary", "television history",
			"genre film", "film noir", "horror film", "science fiction film"}) {
			seeds = append(seeds,
				"https://www.criterion.com/current",
				"https://www.britannica.com/art/film",
				"https://www.filmsite.org/",
				"https://www.afi.com/afis-100-years-100-movies/",
				"https://www.rogerebert.com/great-movies",
				"https://www.screenonline.org.uk/film/",
				"https://silentera.com/",
				"https://www.bfi.org.uk/features/",
			)
		}

		// Numismatics / coins / medals / currency history
		if containsAny(kwLower, []string{"numismatic", "coin", "medal", "currency", "money history",
			"mint", "coinage", "ancient coin", "roman coin", "greek coin",
			"banknote", "paper money", "gold standard", "inflation history",
			"monetary history", "token", "bullion"}) {
			seeds = append(seeds,
				"https://www.numismaticnews.net/",
				"https://www.pcgs.com/coinfacts",
				"https://www.ngccoin.com/",
				"https://www.coinworld.com/",
				"https://www.britishmuseum.org/collection/term/x7699",
				"https://coins.nd.edu/",
				"https://www.smb.museum/en/museums-institutions/muenzkabinett/home/",
				"https://www.money.org/",
			)
		}

		// Chess / board games / game theory
		if containsAny(kwLower, []string{"chess", "game theory", "board game", "go game", "shogi",
			"backgammon", "checkers", "strategy game", "opening theory",
			"endgame chess", "middlegame", "grandmaster", "tournament chess",
			"chess history", "chess engine", "combinatorial game"}) {
			seeds = append(seeds,
				"https://www.chessgames.com/",
				"https://www.chess.com/learn-how-to-play-chess",
				"https://lichess.org/learn",
				"https://www.chessprogramming.org/",
				"https://www.365chess.com/",
				"https://www.thechessworld.com/",
				"https://plato.stanford.edu/entries/game-theory/",
				"https://www.britannica.com/topic/chess",
			)
		}

		// Music theory / composition / instruments
		if containsAny(kwLower, []string{"music theory", "composition", "harmony", "counterpoint",
			"rhythm", "notation", "interval", "chord", "scale", "mode",
			"fugue", "sonata", "symphony", "concerto", "orchestration",
			"instrument", "piano", "violin", "guitar", "trumpet", "organ",
			"music history", "baroque music", "classical music", "romantic music"}) {
			seeds = append(seeds,
				"https://www.musictheory.net/",
				"https://www.teoria.com/",
				"https://www.musictheoryacademy.com/",
				"https://www.britannica.com/art/music",
				"https://www.allmusic.com/",
				"https://www.bach-cantatas.com/",
				"https://imslp.org/",
				"https://www.essentialsofjazztheory.com/",
				"https://www.classiccat.net/",
			)
		}

		// Cryptography / information security / network security
		if containsAny(kwLower, []string{"cryptograph", "cipher", "encryption", "decryption",
			"public key", "rsa", "aes", "hash function", "digital signature",
			"blockchain", "zero knowledge proof", "elliptic curve",
			"symmetric encryption", "asymmetric encryption", "ssl", "tls",
			"cybersecurity", "network security", "firewall", "intrusion detection",
			"malware", "exploit", "vulnerability", "penetration testing",
			"information theory", "shannon entropy", "coding theory"}) {
			seeds = append(seeds,
				"https://www.khanacademy.org/computing/computer-science/cryptography",
				"https://cryptography.io/en/latest/",
				"https://www.crypto101.io/",
				"https://csrc.nist.gov/publications/",
				"https://www.schneier.com/crypto-gram/",
				"https://explained-from-first-principles.com/number-theory/",
				"https://www.cryptomuseum.com/",
				"https://www.owasp.org/index.php/",
				"https://ocw.mit.edu/courses/6-875-cryptography-and-cryptanalysis-spring-2005/",
				"https://www.cs.ucdavis.edu/~rogaway/classes/",
			)
		}

		// Acoustics / sound / vibration / music acoustics
		if containsAny(kwLower, []string{"acoustic", "sound wave", "vibration", "resonance",
			"frequency", "amplitude", "decibel", "ultrasound", "infrasound",
			"room acoustic", "soundproofing", "noise control", "psychoacoustic",
			"music acoustics", "instrument acoustics", "architectural acoustic",
			"sonar", "echolocation", "doppler effect", "standing wave",
			"fourier acoustic", "waveform", "harmonic"}) {
			seeds = append(seeds,
				"https://www.acs.psu.edu/drussell/demos.html",
				"https://www.physicsclassroom.com/class/sound",
				"https://www.britannica.com/science/acoustics",
				"https://hyperphysics.phy-astr.gsu.edu/hbase/Sound/soucon.html",
				"https://www.acoustics.org/press/",
				"https://www.engineeringtoolbox.com/sound-d_56.html",
				"https://newt.phys.unsw.edu.au/music/",
				"https://www.khanacademy.org/science/physics/mechanical-waves-and-sound",
			)
		}

		// Optics / light / photonics / optical instruments
		if containsAny(kwLower, []string{"optic", "light wave", "refraction", "reflection",
			"diffraction", "interference", "polarization", "lens", "prism",
			"telescope", "microscope", "spectroscop", "optical fiber",
			"laser physics", "holography", "quantum optic", "nonlinear optic",
			"geometrical optic", "physical optic", "wave optic", "rainbow",
			"birefringence", "fresnel", "snell"}) {
			seeds = append(seeds,
				"https://hyperphysics.phy-astr.gsu.edu/hbase/ligcon.html",
				"https://www.physicsclassroom.com/class/light",
				"https://www.britannica.com/science/optics",
				"https://www.khanacademy.org/science/physics/geometric-optics",
				"https://ocw.mit.edu/courses/res-6-006-video-demonstrations-in-lasers-and-optics-spring-2008/",
				"https://www.olympus-lifescience.com/en/microscope-resource/",
				"https://www.rp-photonics.com/encyclopedia.html",
			)
		}

		// Cartography / map making / GIS / spatial analysis
		if containsAny(kwLower, []string{"cartograph", "map making", "gis", "spatial analysis",
			"map projection", "coordinate system", "datum", "geospatial",
			"topographic map", "thematic map", "remote sensing", "satellite imagery",
			"lidar", "aerial photography", "land survey", "geodesy",
			"mercator", "robinson projection", "geographic information"}) {
			seeds = append(seeds,
				"https://www.esri.com/en-us/what-is-gis/overview",
				"https://www.geographyrealm.com/",
				"https://www.nationalgeographic.org/encyclopedia/map/",
				"https://www.usgs.gov/programs/national-geospatial-program",
				"https://www.davidrumsey.com/",
				"https://www.maphistory.info/",
				"https://spatialreference.org/",
				"https://gisgeography.com/",
			)
		}

		// Ancient & classical languages — Latin, Greek, Sanskrit, Sumerian
		if containsAny(kwLower, []string{"latin language", "ancient greek language", "sanskrit",
			"sumerian language", "akkadian", "hieroglyphics", "cuneiform",
			"ancient hebrew", "aramaic", "classical arabic", "old english",
			"middle english", "old norse language", "proto-germanic",
			"dead language", "extinct language", "ancient language",
			"latin grammar", "greek grammar", "classical text"}) {
			seeds = append(seeds,
				"https://www.thelatinlibrary.com/",
				"https://www.perseus.tufts.edu/hopper/",
				"https://www.latin-is-simple.com/",
				"https://www.omniglot.com/writing/",
				"https://www.ancientgreek.eu/",
				"https://www.sacred-texts.com/cla/",
				"https://www.bl.uk/ancient-languages",
				"https://www.etymonline.com/",
				"https://digitalhumanities.org/",
			)
		}

		// Comparative literature / world literature / narrative theory
		if containsAny(kwLower, []string{"comparative literature", "world literature", "narrative theory",
			"literary criticism", "literary theory", "structuralism", "poststructuralism",
			"postcolonial literature", "magical realism", "modernist literature",
			"postmodern literature", "epic poetry", "tragedy drama",
			"novel history", "short story", "literary genre",
			"homer", "dante", "shakespeare", "cervantes", "dostoevsky",
			"tolstoy", "kafka", "borges", "garcia marquez", "woolf"}) {
			seeds = append(seeds,
				"https://www.britannica.com/art/literature",
				"https://www.poetryfoundation.org/",
				"https://www.poetryarchive.org/",
				"https://www.gutenberg.org/",
				"https://www.sparknotes.com/lit/",
				"https://www.litcharts.com/",
				"https://www.cliffsnotes.com/literature",
				"https://plato.stanford.edu/entries/poetics/",
				"https://www.oxfordbibliographies.com/browse?module_0=obo-9780190221911",
			)
		}

		// Genomics / bioinformatics / molecular genetics
		if containsAny(kwLower, []string{"genomic", "bioinformatic", "dna sequencing", "rna sequencing",
			"genome assembly", "gene expression", "transcriptome", "proteome",
			"crispr", "gene editing", "epigenetics", "methylation",
			"phylogenomics", "metagenomics", "single cell sequencing",
			"genome wide association", "gwas", "snp", "structural variant",
			"protein structure prediction", "alphafold", "sequence alignment"}) {
			seeds = append(seeds,
				"https://www.ncbi.nlm.nih.gov/",
				"https://www.ebi.ac.uk/training/",
				"https://www.genome.gov/about-genomics/educational-resources",
				"https://www.bioinformatics.org/",
				"https://www.khanacademy.org/science/ap-biology/gene-expression-and-regulation",
				"https://www.coursera.org/learn/bioinformatics",
				"https://biopython.org/DIST/docs/tutorial/",
				"https://www.sanger.ac.uk/science/",
				"https://www.broadinstitute.org/genomics",
			)
		}

		// Thermodynamics & heat transfer (engineering applied)
		if containsAny(kwLower, []string{"heat transfer", "conduction", "convection heat", "radiation heat",
			"heat exchanger design", "fouling", "nusselt number", "prandtl number",
			"boiling heat transfer", "condensation", "thermal resistance",
			"fins heat", "heat sink", "thermal management", "insulation thermal",
			"stefan boltzmann", "blackbody radiation", "kirchhoff thermal"}) {
			seeds = append(seeds,
				"https://www.engineeringtoolbox.com/heat-transfer-d_431.html",
				"https://ocw.mit.edu/courses/2-51-intermediate-heat-and-mass-transfer-fall-2008/",
				"https://www.thermopedia.com/",
				"https://www.engineersedge.com/heat_transfer/",
				"https://hyperphysics.phy-astr.gsu.edu/hbase/thermo/heattrans.html",
				"https://www.efunda.com/formulae/heat_transfer/",
			)
		}

		// Control theory / systems engineering / automation
		if containsAny(kwLower, []string{"control theory", "feedback control", "pid control",
			"transfer function", "laplace transform control", "bode plot",
			"nyquist", "root locus", "state space", "optimal control",
			"model predictive control", "mpc", "robust control",
			"automation", "plc", "scada", "process control",
			"systems engineering", "systems thinking"}) {
			seeds = append(seeds,
				"https://ocw.mit.edu/courses/6-302-feedback-systems-spring-2007/",
				"https://www.britannica.com/technology/automation",
				"https://www.controleng.com/",
				"https://www.engineeringtoolbox.com/",
				"https://ctms.engin.umich.edu/CTMS/",
				"https://www.ni.com/en-us/innovations/white-papers/06/pid-theory-explained.html",
				"https://ocw.mit.edu/courses/2-14-analysis-and-design-of-feedback-control-systems-spring-2014/",
			)
		}

		// Renewable energy / solar / wind / energy storage
		if containsAny(kwLower, []string{"renewable energy", "solar panel", "photovoltaic", "wind turbine",
			"energy storage", "battery technology", "lithium ion", "fuel cell",
			"hydrogen energy", "geothermal energy", "tidal energy", "wave energy",
			"smart grid", "energy efficiency", "heat pump", "solar thermal",
			"offshore wind", "wind farm", "solar farm", "power purchase"}) {
			seeds = append(seeds,
				"https://www.energy.gov/eere/office-energy-efficiency-renewable-energy",
				"https://www.irena.org/publications",
				"https://www.nrel.gov/research/",
				"https://www.iea.org/topics/renewables",
				"https://www.solarpowereurope.org/",
				"https://www.windpowermonthly.com/",
				"https://www.pv-magazine.com/",
				"https://www.energystoragenews.com/",
				"https://www.eia.gov/renewable/",
			)
		}

		// Archaeology — methods, excavation, dating techniques
		if containsAny(kwLower, []string{"archaeolog", "excavation", "artifact", "radiocarbon dating",
			"stratigraphy archaeol", "lithic", "ceramic analysis", "dendrochronology",
			"aerial archaeology", "underwater archaeology", "maritime archaeology",
			"zooarchaeology", "archaeobotany", "human remains", "burial site",
			"stonehenge", "pompeii", "troy", "machu picchu", "angkor"}) {
			seeds = append(seeds,
				"https://www.archaeology.org/",
				"https://www.britishmuseum.org/learn/schools/ages-7-11/ancient-greece",
				"https://www.worldarchaeology.com/",
				"https://www.smithsonianmag.com/category/history-archaeology/",
				"https://www.worldhistory.org/archaeology/",
				"https://archaeology.about.com/",
				"https://www.nps.gov/archeology/",
				"https://www.archaeologydataservice.ac.uk/",
			)
		}

		// Cognitive science — perception, attention, decision making, AI cognition
		if containsAny(kwLower, []string{"perception", "attention cognitive", "working memory",
			"executive function", "cognitive load", "dual process theory",
			"heuristic", "cognitive bias", "judgment decision",
			"visual perception", "auditory perception", "proprioception",
			"embodied cognition", "situated cognition", "predictive coding",
			"cognitive architecture", "act-r", "connectionism",
			"computational neuroscience", "neural coding"}) {
			seeds = append(seeds,
				"https://www.cogsci.ucsd.edu/",
				"https://plato.stanford.edu/entries/cognitive-science/",
				"https://www.scholarpedia.org/article/Cognitive_science",
				"https://noba.to/",
				"https://www.psychologytoday.com/us/basics/cognition",
				"https://ocw.mit.edu/courses/9-00sc-introduction-to-psychology-fall-2011/",
				"https://www.frontiersin.org/journals/cognitive-science",
				"https://onlinelibrary.wiley.com/journal/15516709",
			)
		}

		// Economics — financial markets, banking, monetary policy
		if containsAny(kwLower, []string{"financial market", "stock market", "bond market",
			"monetary policy", "central bank", "interest rate", "inflation economics",
			"banking system", "fractional reserve", "quantitative easing",
			"exchange rate", "balance of payments", "trade deficit",
			"derivatives", "options pricing", "futures market",
			"keynesian economics", "monetarism", "austrian economics",
			"development economics", "international trade theory"}) {
			seeds = append(seeds,
				"https://www.econlib.org/",
				"https://www.federalreserve.gov/education.htm",
				"https://www.imf.org/en/Publications/",
				"https://www.worldbank.org/en/research",
				"https://www.khanacademy.org/economics-finance-domain",
				"https://www.stlouisfed.org/education/",
				"https://www.investopedia.com/financial-term-dictionary-4769738",
				"https://plato.stanford.edu/entries/economics/",
				"https://www.nber.org/",
			)
		}

		// Demography / population studies / urban sociology
		if containsAny(kwLower, []string{"demograph", "population growth", "fertility rate",
			"mortality rate", "migration", "urbanization", "census",
			"age structure", "dependency ratio", "demographic transition",
			"population pyramid", "life expectancy", "infant mortality",
			"urban sociology", "suburbanization", "gentrification",
			"housing market", "city growth", "megacity"}) {
			seeds = append(seeds,
				"https://www.prb.org/resources/",
				"https://ourworldindata.org/population",
				"https://www.un.org/en/global-issues/population",
				"https://www.census.gov/topics/population.html",
				"https://www.worldometers.info/",
				"https://www.oecd.org/social/society-at-a-glance.htm",
				"https://www.pewresearch.org/topic/demographics-age/",
			)
		}

		// Nanotechnology / materials at nanoscale
		if containsAny(kwLower, []string{"nanotechnology", "nanoscale", "nanoparticle",
			"carbon nanotube", "graphene", "quantum dot", "nanomaterial",
			"self assembly", "molecular machine", "nano fabrication",
			"atomic force microscopy", "scanning tunneling microscope",
			"nano medicine", "drug nanoparticle", "nano sensor"}) {
			seeds = append(seeds,
				"https://www.nano.gov/nanotech-101/what/nano-size",
				"https://www.nature.com/subjects/nanotechnology",
				"https://www.nanowerk.com/nanotechnology/introduction/",
				"https://www.scientificamerican.com/nanotechnology/",
				"https://www.azonano.com/nanotechnology.aspx",
				"https://www.rsc.org/periodic-table/",
			)
		}

		// Artificial intelligence / machine learning / deep learning
		if containsAny(kwLower, []string{"artificial intelligence", "machine learning", "deep learning",
			"neural network", "natural language processing", "nlp", "computer vision",
			"reinforcement learning", "transformer model", "large language model",
			"convolutional neural", "recurrent neural", "backpropagation",
			"gradient descent", "overfitting", "generalization", "training data",
			"supervised learning", "unsupervised learning", "generative ai",
			"gan", "diffusion model", "attention mechanism", "bert", "gpt"}) {
			seeds = append(seeds,
				"https://www.deeplearningbook.org/",
				"https://cs231n.github.io/",
				"https://karpathy.github.io/",
				"https://www.fast.ai/",
				"https://d2l.ai/",
				"https://distill.pub/",
				"https://www.nature.com/articles/nature14539",
				"https://paperswithcode.com/methods",
				"https://huggingface.co/learn",
				"https://www.deepmind.com/research",
				"https://openai.com/research/",
				"https://www.ibm.com/topics/artificial-intelligence",
			)
		}

		// Agriculture / crop science / soil science / farming
		if containsAny(kwLower, []string{"agriculture", "crop science", "soil science", "agronomy",
			"irrigation", "fertilizer", "pesticide", "organic farming",
			"permaculture", "precision agriculture", "livestock", "poultry",
			"aquaculture", "silviculture", "agroforestry", "seed saving",
			"plant breeding", "gmo crops", "crop rotation", "soil health",
			"composting", "hydroponics", "vertical farming", "food production"}) {
			seeds = append(seeds,
				"https://www.fao.org/agriculture/",
				"https://www.ers.usda.gov/topics/",
				"https://www.nal.usda.gov/",
				"https://www.rothamsted.ac.uk/",
				"https://www.sare.org/resources/",
				"https://soilhealth.cals.cornell.edu/",
				"https://www.agronomy.org/",
				"https://extension.psu.edu/agronomy",
				"https://www.fao.org/soils-portal/en/",
			)
		}

		// Mycology / fungi / mushrooms / fungal ecology
		if containsAny(kwLower, []string{"mycolog", "fungi", "fungus", "mushroom", "mold",
			"yeast", "lichen", "mycelium", "spore", "fruiting body",
			"decomposer", "symbiosis fungal", "mycorrhiza", "endophyte",
			"pathogenic fungi", "fermentation fungi", "truffle", "penicillin",
			"fungal disease", "athlete foot", "aspergillus", "candida"}) {
			seeds = append(seeds,
				"https://www.mycobank.org/",
				"https://www.ars.usda.gov/research/areas/plant-protection/invasive-species-and-biocontrol/",
				"https://nature.berkeley.edu/brunslab/",
				"https://www.mushroomexpert.com/",
				"https://www.nybg.org/plant-research-and-conservation/mycology/",
				"https://www.britannica.com/science/fungus",
				"https://www.khanacademy.org/science/ap-biology/ecology-ap/community-ecology-ap/a/fungi",
				"https://www.inaturalist.org/taxa/47170-Fungi",
			)
		}

		// Virology — virus biology, replication, viral diseases
		if containsAny(kwLower, []string{"virology", "virus structure", "viral replication",
			"bacteriophage", "retrovirus", "influenza virus", "coronavirus",
			"hiv aids", "ebola", "herpes virus", "adenovirus", "poxvirus",
			"viral evolution", "antiviral", "host tropism", "capsid",
			"envelope protein", "spike protein", "rna virus", "dna virus",
			"viral genome", "zoonotic virus", "emerging virus"}) {
			seeds = append(seeds,
				"https://www.virology.ws/",
				"https://www.ncbi.nlm.nih.gov/books/NBK8174/",
				"https://www.cdc.gov/other/language.html",
				"https://www.who.int/news-room/fact-sheets",
				"https://www.khanacademy.org/science/ap-biology/gene-expression-and-regulation/viruses-ap/",
				"https://www.microbiologyonline.org/about-microbiology/introducing-virology",
				"https://viralzone.expasy.org/",
				"https://www.nature.com/subjects/virology",
			)
		}

		// Immunology deep — adaptive immunity, antibodies, vaccines
		if containsAny(kwLower, []string{"adaptive immunity", "innate immunity", "b cell", "t cell",
			"antibody", "immunoglobulin", "antigen", "mhc", "cytokine",
			"lymphocyte", "macrophage", "dendritic cell", "natural killer",
			"complement system", "inflammation", "autoimmune", "allergy",
			"hypersensitivity", "immunodeficiency", "transplant rejection",
			"vaccine mechanism", "adjuvant", "memory cell"}) {
			seeds = append(seeds,
				"https://www.immunology.org/public-information/",
				"https://www.khanacademy.org/science/ap-biology/natural-selection/history-of-life-on-earth-ap/",
				"https://www.ncbi.nlm.nih.gov/books/NBK279364/",
				"https://www.britannica.com/science/immune-system",
				"https://www.nature.com/subjects/immunology",
				"https://www.immunologyeducation.org/",
				"https://ocw.mit.edu/courses/7-342-the-immune-system-and-the-developing-brain-fall-2004/",
				"https://www.frontiersin.org/journals/immunology",
			)
		}

		// Endocrinology — hormones, glands, metabolism
		if containsAny(kwLower, []string{"endocrinolog", "hormone", "gland", "insulin", "diabetes",
			"thyroid", "adrenal", "cortisol", "testosterone", "estrogen",
			"growth hormone", "pituitary", "hypothalamus endocrine",
			"pancreas", "parathyroid", "pineal gland", "melatonin",
			"feedback loop endocrine", "receptor hormone", "steroid hormone",
			"peptide hormone", "metabolic syndrome"}) {
			seeds = append(seeds,
				"https://www.hormone.org/your-health-and-hormones/",
				"https://www.britannica.com/science/endocrine-system",
				"https://www.khanacademy.org/science/ap-biology/cell-communication-and-cell-cycle/",
				"https://www.ncbi.nlm.nih.gov/books/NBK278946/",
				"https://www.endocrine.org/patient-engagement/endocrine-library",
				"https://www.merckmanuals.com/home/hormonal-and-metabolic-disorders",
				"https://ocw.mit.edu/courses/9-14-brain-structure-and-its-origins-spring-2014/",
			)
		}

		// Sleep science / circadian rhythm / sleep disorders
		if containsAny(kwLower, []string{"sleep science", "circadian rhythm", "sleep disorder",
			"insomnia", "sleep apnea", "rem sleep", "slow wave sleep",
			"sleep stage", "melatonin sleep", "chronobiology", "jet lag",
			"narcolepsy", "sleep deprivation", "polysomnography",
			"dream", "memory consolidation sleep", "glymphatic system"}) {
			seeds = append(seeds,
				"https://www.sleepfoundation.org/sleep-science",
				"https://www.ncbi.nlm.nih.gov/books/NBK279544/",
				"https://www.ninds.nih.gov/health-information/public-education/brain-basics/brain-basics-understanding-sleep",
				"https://www.britannica.com/science/sleep-biological-state",
				"https://sleepeducation.org/",
				"https://www.nature.com/subjects/sleep",
				"https://hubermanlab.com/sleep/",
			)
		}

		// Gut microbiome / microbiota / probiotics
		if containsAny(kwLower, []string{"gut microbiome", "microbiota", "probiotic", "prebiotic",
			"intestinal flora", "dysbiosis", "firmicutes", "bacteroidetes",
			"akkermansia", "lactobacillus", "bifidobacterium",
			"gut brain axis", "short chain fatty acid", "leaky gut",
			"fecal transplant", "16s sequencing microbiome",
			"microbiome diversity", "antibiotic microbiome"}) {
			seeds = append(seeds,
				"https://www.nature.com/subjects/microbiome",
				"https://www.humanfoodproject.com/",
				"https://microbiome.nih.gov/",
				"https://www.sciencedirect.com/topics/immunology-and-microbiology/gut-microbiome",
				"https://www.broadinstitute.org/microbiome",
				"https://www.ebi.ac.uk/metagenomics/",
				"https://www.gutmicrobiotaforhealth.com/",
			)
		}

		// Protein biochemistry / enzymes / structural biology
		if containsAny(kwLower, []string{"protein folding", "protein structure", "enzyme kinetics",
			"michaelis menten", "enzyme catalysis", "active site",
			"allosteric regulation", "cofactor", "coenzyme",
			"structural biology", "x-ray crystallography protein",
			"cryo electron microscopy", "nmr protein", "alphafold",
			"proteomics", "mass spectrometry protein", "amino acid",
			"peptide bond", "protein domain", "secondary structure protein"}) {
			seeds = append(seeds,
				"https://www.rcsb.org/",
				"https://www.ncbi.nlm.nih.gov/books/NBK26830/",
				"https://www.khanacademy.org/science/ap-biology/gene-expression-and-regulation/",
				"https://www.britannica.com/science/protein",
				"https://www.ebi.ac.uk/training/online/courses/protein-classification/",
				"https://alphafold.ebi.ac.uk/",
				"https://www.ncbi.nlm.nih.gov/Structure/",
				"https://www.nature.com/subjects/structural-biology",
			)
		}

		// Cell biology deep — signaling, division, organelles
		if containsAny(kwLower, []string{"cell signaling", "signal transduction", "second messenger",
			"cell division", "mitosis", "meiosis", "cell cycle",
			"organelle", "mitochondria", "endoplasmic reticulum", "golgi",
			"lysosome", "ribosome", "cytoskeleton", "actin", "tubulin",
			"cell membrane", "membrane transport", "ion channel",
			"apoptosis", "autophagy", "endocytosis", "exocytosis"}) {
			seeds = append(seeds,
				"https://www.khanacademy.org/science/ap-biology/cell-structure-and-function/",
				"https://www.ncbi.nlm.nih.gov/books/NBK9963/",
				"https://www.britannica.com/science/cell-biology",
				"https://www.nature.com/subjects/cell-biology",
				"https://www.cellbio.org/",
				"https://www.microscopyu.com/tutorials/",
				"https://www.ibiology.org/",
			)
		}

		// Cancer biology / oncology / tumor immunology
		if containsAny(kwLower, []string{"cancer biology", "tumor", "oncogene", "tumor suppressor",
			"metastasis", "angiogenesis tumor", "cancer immunotherapy",
			"checkpoint inhibitor", "car-t cell", "chemotherapy mechanism",
			"radiation therapy biology", "carcinogen", "mutation cancer",
			"cancer stem cell", "liquid biopsy", "cancer screening",
			"hallmarks of cancer", "warburg effect"}) {
			seeds = append(seeds,
				"https://www.cancer.gov/about-cancer/understanding/",
				"https://www.nature.com/subjects/cancer",
				"https://www.ncbi.nlm.nih.gov/books/NBK9963/",
				"https://www.cancer.org/cancer/understanding-cancer/",
				"https://www.cancerresearch.org/immunotherapy/",
				"https://www.aacrjournals.org/",
				"https://www.ted.com/topics/cancer",
				"https://ocw.mit.edu/courses/7-012-introduction-to-biology-fall-2004/",
			)
		}

		// Paleontology / dinosaurs / mass extinctions / Cambrian
		if containsAny(kwLower, []string{"dinosaur", "theropod", "sauropod", "pterosaur",
			"ichthyosaur", "plesiosaur", "triceratops", "tyrannosaurus",
			"cambrian explosion", "mass extinction", "permian extinction",
			"cretaceous extinction", "triassic", "jurassic", "cretaceous",
			"paleocene", "eocene", "fossil record", "trace fossil",
			"amber fossil", "trilobite", "ammonite", "early life"}) {
			seeds = append(seeds,
				"https://www.nhm.ac.uk/discover/dinosaurs.html",
				"https://paleobiodb.org/",
				"https://www.britannica.com/animal/dinosaur",
				"https://www.smithsonianmag.com/category/science-nature/",
				"https://ucmp.berkeley.edu/",
				"https://www.prehistoric-wildlife.com/",
				"https://www.amnh.org/research/paleontology",
				"https://www.newdinosaurs.com/",
			)
		}

		// Primatology / great apes / human evolution
		if containsAny(kwLower, []string{"primatolog", "great ape", "chimpanzee", "gorilla",
			"orangutan", "bonobo", "gibbon", "monkey", "primate behavior",
			"primate cognition", "primate tool use", "human evolution",
			"hominin", "australopithecus", "homo erectus", "neanderthal",
			"homo sapiens origin", "out of africa", "mitochondrial eve",
			"primate communication", "jane goodall"}) {
			seeds = append(seeds,
				"https://www.janegoodall.org/our-story/about-jane/",
				"https://www.britannica.com/animal/primate-mammal",
				"https://www.smithsonianmag.com/category/human-origins/",
				"https://humanorigins.si.edu/",
				"https://www.nature.com/subjects/primatology",
				"https://www.primate-sg.org/",
				"https://www.sciencedirect.com/topics/agricultural-and-biological-sciences/primatology",
			)
		}

		// Deep sea / ocean floor / hydrothermal vents
		if containsAny(kwLower, []string{"deep sea", "ocean floor", "hydrothermal vent",
			"abyssal zone", "hadal zone", "bathypelagic", "bioluminescence",
			"deep sea creature", "anglerfish", "giant squid", "deep sea fish",
			"ocean trench", "mariana trench", "black smoker", "chemosynthesis",
			"deep sea exploration", "submersible", "benthic zone"}) {
			seeds = append(seeds,
				"https://ocean.si.edu/ocean-life/fish/deep-sea",
				"https://www.mbari.org/science/",
				"https://oceanexplorer.noaa.gov/",
				"https://www.whoi.edu/know-your-ocean/",
				"https://www.britannica.com/science/deep-sea-fauna",
				"https://www.sciencedaily.com/terms/deep_sea.htm",
				"https://www.ifremer.fr/en/",
			)
		}

		// Polar regions / Arctic / Antarctic / polar ecology
		if containsAny(kwLower, []string{"arctic", "antarctic", "polar", "tundra ecology",
			"permafrost", "sea ice", "polar bear", "penguin", "seal arctic",
			"polar exploration history", "amundsen", "shackleton", "scott antarctic",
			"ice sheet", "glacier", "cryosphere", "polar climate",
			"midnight sun", "aurora borealis", "northern lights"}) {
			seeds = append(seeds,
				"https://www.bas.ac.uk/",
				"https://nsidc.org/learn/parts-cryosphere/",
				"https://www.nationalgeographic.com/environment/article/arctic",
				"https://www.coolantarctica.com/",
				"https://www.spri.cam.ac.uk/",
				"https://www.npolar.no/en/",
				"https://www.britannica.com/place/Antarctic",
				"https://oceanservice.noaa.gov/facts/arctic.html",
			)
		}

		// Rainforest / tropical ecology / Amazon / biodiversity hotspots
		if containsAny(kwLower, []string{"rainforest", "tropical forest", "amazon", "congo rainforest",
			"borneo", "canopy layer", "understory", "biodiversity hotspot",
			"deforestation", "slash burn", "indigenous rainforest",
			"epiphyte", "bromeliad", "orchid tropical", "jaguar", "toucan",
			"tropical ecology", "forest carbon", "REDD"}) {
			seeds = append(seeds,
				"https://www.worldwildlife.org/biomes/tropical-and-subtropical-moist-broadleaf-forests",
				"https://rainforests.mongabay.com/",
				"https://www.rainforest-alliance.org/insights/",
				"https://www.nationalgeographic.com/environment/habitats/rain-forests/",
				"https://www.amnh.org/research/center-for-biodiversity-conservation",
				"https://www.iucn.org/resources/issues-briefs/forests-and-climate-change",
			)
		}

		// Forensic science / forensic chemistry / criminalistics
		if containsAny(kwLower, []string{"forensic science", "forensic chemistry", "criminalistics",
			"crime scene", "dna forensics", "fingerprint", "ballistics",
			"toxicology forensic", "forensic anthropology", "forensic pathology",
			"blood spatter", "digital forensics", "document examination",
			"forensic entomology", "time of death", "arson investigation"}) {
			seeds = append(seeds,
				"https://www.britannica.com/topic/forensic-science",
				"https://www.fbi.gov/services/laboratory/",
				"https://www.aafs.org/resources/",
				"https://www.ncfs.ucf.edu/",
				"https://www.ojp.gov/ncjrs/virtual-library/forensic-science",
				"https://www.nist.gov/forensics",
				"https://www.forensicscienceinternational.com/",
			)
		}

		// Telecommunications history / radio / telegraph / telephone
		if containsAny(kwLower, []string{"telecommunication", "telegraph", "telephone history",
			"radio history", "marconi", "morse code", "radio wave",
			"am fm radio", "television history", "satellite communication",
			"undersea cable", "fiber optic communication", "internet history",
			"arpanet", "ethernet", "wireless communication", "4g 5g",
			"cell phone history", "spectrum allocation"}) {
			seeds = append(seeds,
				"https://www.britannica.com/technology/telecommunications",
				"https://ethw.org/Main_Page",
				"https://www.historyoftelecommunications.net/",
				"https://www.itu.int/en/history/",
				"https://www.computerhistory.org/",
				"https://www.ieee.org/about/ieee-history.html",
				"https://www.sciencemuseum.org.uk/objects-and-stories/chemistry/telecommunications",
			)
		}

		// Fashion / textile history / clothing design
		if containsAny(kwLower, []string{"fashion history", "textile history", "clothing design",
			"fabric", "weaving", "dyeing textile", "silk road textile",
			"haute couture", "ready to wear", "fashion designer",
			"costume history", "medieval clothing", "victorian fashion",
			"1920s fashion", "fashion industry", "sustainable fashion",
			"natural fiber", "synthetic fiber", "loom", "knitting history"}) {
			seeds = append(seeds,
				"https://www.metmuseum.org/toah/hi/te_index/hidf.htm",
				"https://www.vam.ac.uk/collections/fashion",
				"https://www.fashionencyclopedia.com/",
				"https://www.britannica.com/topic/fashion",
				"https://www.fitnyc.edu/museum/",
				"https://fashionhistory.fitnyc.edu/",
				"https://www.textilelearner.net/",
				"https://www.lookingglass.fashionhistory.fitnyc.edu/",
			)
		}

		// Video games / game design / game history
		if containsAny(kwLower, []string{"video game", "game design", "game development",
			"game history", "arcade game", "console history", "atari",
			"nintendo", "playstation", "game engine", "level design",
			"game mechanics", "ludonarrative", "indie game", "esports",
			"game theory ludology", "procedural generation", "pixel art",
			"8 bit gaming", "game ai", "pathfinding game"}) {
			seeds = append(seeds,
				"https://www.gamedeveloper.com/",
				"https://www.gdcvault.com/free/",
				"https://www.gameinformer.com/",
				"https://www.britannica.com/technology/video-game",
				"https://www.computerhistory.org/revolution/computer-games/",
				"https://www.museumofplay.org/",
				"https://www.gamespot.com/articles/",
				"https://www.giantbomb.com/wiki/",
			)
		}

		// Typography / graphic design / visual communication
		if containsAny(kwLower, []string{"typography", "typeface", "font", "graphic design",
			"visual design", "logo design", "brand identity", "layout design",
			"color theory", "gestalt", "bauhaus design", "swiss design",
			"modernist design", "poster design", "book design",
			"type history", "movable type", "letterpress", "calligraphy"}) {
			seeds = append(seeds,
				"https://www.fonts.com/content/learning",
				"https://www.designhistory.org/",
				"https://www.aiga.org/",
				"https://www.ilovetypography.com/",
				"https://www.smashingmagazine.com/category/typography/",
				"https://www.britannica.com/topic/typography",
				"https://typographica.org/",
				"https://www.printmag.com/",
			)
		}

		// Computer hardware / CPU / memory / storage architecture
		if containsAny(kwLower, []string{"cpu architecture", "processor design", "instruction set",
			"x86", "arm architecture", "risc", "cisc", "pipeline cpu",
			"cache memory", "ram", "dram", "sram", "flash memory",
			"ssd storage", "hard disk", "memory hierarchy",
			"von neumann", "harvard architecture", "fpga design",
			"gpu architecture", "parallel computing hardware",
			"motherboard", "memory bus", "pcie"}) {
			seeds = append(seeds,
				"https://www.computerhistory.org/",
				"https://ocw.mit.edu/courses/6-004-computation-structures-spring-2017/",
				"https://www.cs.umd.edu/class/fall2018/cmsc411/",
				"https://www.anandtech.com/",
				"https://www.techpowerup.com/",
				"https://www.extremetech.com/",
				"https://www.britannica.com/technology/computer/History-of-computing",
				"https://www.intel.com/content/www/us/en/history/",
			)
		}

		// International relations / geopolitics / diplomacy
		if containsAny(kwLower, []string{"international relations", "geopolitic", "diplomacy",
			"foreign policy", "realism ir", "liberalism ir", "constructivism ir",
			"balance of power", "hegemony", "sovereignty", "united nations",
			"nato", "treaty", "sanctions", "deterrence", "nuclear deterrence",
			"security dilemma", "soft power", "hard power", "proxy war"}) {
			seeds = append(seeds,
				"https://www.cfr.org/",
				"https://www.foreignaffairs.com/",
				"https://www.chathamhouse.org/",
				"https://www.brookings.edu/topic/foreign-policy/",
				"https://plato.stanford.edu/entries/war/",
				"https://www.un.org/en/global-issues/",
				"https://www.britannica.com/topic/international-relations",
				"https://www.e-ir.info/",
			)
		}

		// Labor history / trade unions / workers rights
		if containsAny(kwLower, []string{"labor history", "trade union", "workers rights",
			"strike", "collective bargaining", "labor movement",
			"industrial dispute", "child labor history", "minimum wage history",
			"eight hour day", "AFL CIO", "IWW", "syndicalism",
			"labor reform", "workplace safety history", "sweatshop",
			"fordism", "taylorism", "labor economics"}) {
			seeds = append(seeds,
				"https://www.britannica.com/topic/labor-movement",
				"https://www.dol.gov/general/aboutdol/history",
				"https://www.historyhit.com/topics/industrial-revolution/",
				"https://www.ilr.cornell.edu/",
				"https://www.history.com/topics/industrial-revolution/labor",
				"https://www.ilo.org/global/about-the-ilo/history/",
				"https://www.workplacefairness.org/",
			)
		}

		// Slavic history / Eastern Europe / Russia / Byzantine east
		if containsAny(kwLower, []string{"slavic history", "russian history", "polish history",
			"czech history", "byzantine", "kievan rus", "mongol invasion rus",
			"russian empire", "soviet union", "cold war soviet",
			"balkan history", "yugoslav", "czechoslovakia", "austro-hungarian",
			"poland partitions", "ukraine history", "orthodox christianity"}) {
			seeds = append(seeds,
				"https://www.britannica.com/topic/history-of-Russia",
				"https://www.historyhit.com/",
				"https://www.bbc.co.uk/history/",
				"https://www.worldhistory.org/",
				"https://www.eurozine.com/",
				"https://www.encyclopedia.com/history/",
			)
		}

		// Latin American history / independence / pre-Columbian
		if containsAny(kwLower, []string{"latin american history", "south american history",
			"spanish colonialism", "portuguese colonialism", "latin independence",
			"simon bolivar", "jose san martin", "mexican revolution",
			"cuban history", "argentina history", "brazil history",
			"inca empire", "aztec history", "maya history", "andes civilization",
			"banana republic", "caudillo", "liberation theology"}) {
			seeds = append(seeds,
				"https://www.britannica.com/place/Latin-America",
				"https://www.worldhistory.org/",
				"https://www.latinamericanstudies.org/",
				"https://library.brown.edu/create/modernlatinamerica/",
				"https://www.smithsonianmag.com/category/history-archaeology/",
				"https://www.historyhit.com/",
			)
		}

		// Southeast Asian / Pacific / Oceanian history
		if containsAny(kwLower, []string{"southeast asia history", "vietnam history", "thailand history",
			"cambodia history", "khmer empire", "indonesia history",
			"philippines history", "myanmar history", "colonialism asia",
			"polynesian history", "maori history", "aboriginal australian",
			"pacific island", "melanesia", "micronesia", "hawaii history",
			"new zealand history"}) {
			seeds = append(seeds,
				"https://www.britannica.com/place/Southeast-Asia",
				"https://www.worldhistory.org/",
				"https://www.abc.net.au/education/",
				"https://www.nzhistory.govt.nz/",
				"https://www.pacifichistory.org/",
				"https://www.soas.ac.uk/",
			)
		}

		// Animal migration / navigation / seasonal movement
		if containsAny(kwLower, []string{"animal migration", "bird migration", "fish migration",
			"salmon run", "wildebeest migration", "monarch butterfly",
			"whale migration", "bat migration", "insect migration",
			"navigation animal", "magnetic navigation", "star navigation bird",
			"homing pigeon", "natal homing", "flyway", "stopover habitat"}) {
			seeds = append(seeds,
				"https://www.allaboutbirds.org/guide/",
				"https://www.nationalgeographic.com/animals/",
				"https://www.audubon.org/",
				"https://www.fs.usda.gov/wildflowers/pollinators/Monarch_Butterfly/",
				"https://www.britannica.com/animal/migration-zoology",
				"https://www.movebank.org/",
				"https://animalmigration.org/",
			)
		}

		// Intelligence / espionage history / cold war spying
		if containsAny(kwLower, []string{"espionage", "intelligence history", "cold war spying",
			"cia history", "kgb", "mi6", "oss", "signals intelligence",
			"human intelligence", "covert operation", "double agent",
			"cryptanalysis", "enigma machine", "bletchley park",
			"aldrich ames", "kim philby", "spy history", "surveillance history"}) {
			seeds = append(seeds,
				"https://www.britannica.com/topic/espionage",
				"https://www.cia.gov/stories/",
				"https://www.nsa.gov/about/cryptologic-heritage/",
				"https://www.nationalww2museum.org/",
				"https://www.historyhit.com/",
				"https://www.spymuseum.org/",
				"https://www.ij.org/",
			)
		}

		// Quantum computing / quantum information
		if containsAny(kwLower, []string{"quantum computing", "qubit", "quantum gate",
			"quantum entanglement", "quantum superposition", "quantum algorithm",
			"shor algorithm", "grover algorithm", "quantum error correction",
			"quantum supremacy", "quantum annealing", "variational quantum",
			"quantum circuit", "bloch sphere", "quantum decoherence",
			"topological qubit", "quantum cryptography", "bb84"}) {
			seeds = append(seeds,
				"https://quantum.country/",
				"https://www.ibm.com/quantum/learn",
				"https://qiskit.org/learn/",
				"https://www.microsoft.com/en-us/research/research-area/quantum-computing/",
				"https://ocw.mit.edu/courses/8-370x-quantum-information-science-i-spring-2018/",
				"https://www.scottaaronson.com/blog/",
				"https://www.nature.com/subjects/quantum-information",
				"https://arxiv.org/abs/quant-ph",
				"https://quantumalgorithmzoo.org/",
			)
		}

		// Computer graphics / rendering / 3D / game engines
		if containsAny(kwLower, []string{"computer graphics", "rendering", "ray tracing",
			"rasterization", "shading", "texture mapping", "global illumination",
			"physically based rendering", "opengl", "vulkan", "directx",
			"3d modeling", "mesh", "polygon", "vertex shader", "fragment shader",
			"animation 3d", "motion capture", "rigging", "skinning",
			"game engine graphics", "real time rendering", "vfx"}) {
			seeds = append(seeds,
				"https://learnopengl.com/",
				"https://www.scratchapixel.com/",
				"https://www.realtimerendering.com/",
				"https://graphicscodex.com/",
				"https://www.pbr-book.org/",
				"https://thebookofshaders.com/",
				"https://www.siggraph.org/learn/",
				"https://www.shadertoy.com/",
			)
		}

		// Epidemics / pandemic history / infectious disease history
		if containsAny(kwLower, []string{"epidemic history", "pandemic history", "plague history",
			"black death", "spanish flu", "cholera pandemic", "smallpox history",
			"yellow fever", "typhus history", "polio history", "malaria history",
			"germ theory history", "quarantine history", "vaccination history",
			"john snow cholera", "pasteur", "koch", "public health history",
			"covid 19 history", "1918 influenza"}) {
			seeds = append(seeds,
				"https://www.history.com/topics/inventions/history-of-medicine",
				"https://www.ncbi.nlm.nih.gov/pmc/articles/PMC7250087/",
				"https://www.cdc.gov/museum/history/",
				"https://www.who.int/about/history/",
				"https://www.smithsonianmag.com/category/science-nature/",
				"https://www.britannica.com/science/history-of-medicine",
				"https://www.pbs.org/newshour/health",
				"https://www.wellcomecollection.org/",
			)
		}

		// Aging biology / longevity / geroscience
		if containsAny(kwLower, []string{"aging biology", "longevity", "geroscience", "senescence",
			"telomere", "hayflick limit", "rapamycin aging", "caloric restriction",
			"autophagy aging", "sirtuin", "nad aging", "inflammaging",
			"blue zones", "centenarian", "lifespan extension",
			"free radical theory aging", "mitochondrial aging",
			"epigenetic clock", "dna damage aging"}) {
			seeds = append(seeds,
				"https://www.nia.nih.gov/research/labs/lci/biology-aging",
				"https://www.sens.org/research/",
				"https://www.longevity.technology/",
				"https://www.science.org/topic/category/aging",
				"https://www.nature.com/subjects/ageing",
				"https://www.bluezones.com/",
				"https://www.lifespan.io/",
				"https://www.ncbi.nlm.nih.gov/books/NBK9943/",
			)
		}

		// Stem cells / regenerative medicine / tissue repair
		if containsAny(kwLower, []string{"stem cell", "pluripotent", "embryonic stem cell",
			"induced pluripotent", "ips cell", "adult stem cell",
			"hematopoietic stem cell", "bone marrow transplant",
			"regenerative medicine", "tissue repair", "wound healing",
			"organ regeneration", "limb regeneration", "organoid",
			"3d bioprinting", "cell therapy", "gene therapy stem"}) {
			seeds = append(seeds,
				"https://www.isscr.org/resources/",
				"https://stemcells.nih.gov/info/basics/",
				"https://www.nature.com/subjects/stem-cells",
				"https://www.eurostemcell.org/",
				"https://www.closerlookatstemcells.org/",
				"https://www.ncbi.nlm.nih.gov/books/NBK27068/",
				"https://www.mda.org/research/neuromuscular-disease-research",
			)
		}

		// Astrobiology / search for life / exoplanet habitability
		if containsAny(kwLower, []string{"astrobiology", "search for life", "exoplanet habitability",
			"habitable zone", "seti", "extremophile", "tardigrade",
			"panspermia", "origin of life", "prebiotic chemistry",
			"rna world", "hydrothermal vent origin", "drake equation",
			"fermi paradox", "biosignature", "technosignature",
			"mars life", "europa ocean", "enceladus", "titan atmosphere"}) {
			seeds = append(seeds,
				"https://astrobiology.nasa.gov/",
				"https://www.seti.org/",
				"https://www.planetary.org/",
				"https://exoplanets.nasa.gov/",
				"https://www.nasa.gov/topics/universe/features/astrobiology/",
				"https://www.liebertpub.com/loi/ast",
				"https://www.britannica.com/science/astrobiology",
				"https://www.scientificamerican.com/astrobiology/",
			)
		}

		// Seismology / earthquake science / geophysics
		if containsAny(kwLower, []string{"seismolog", "earthquake science", "seismic wave",
			"p wave", "s wave", "richter scale", "moment magnitude",
			"fault", "tectonic stress", "subduction zone seismic",
			"seismograph", "seismic hazard", "earthquake prediction",
			"liquefaction", "tsunami origin", "aftershock",
			"earth interior", "mantle seismic", "core seismic"}) {
			seeds = append(seeds,
				"https://www.usgs.gov/natural-hazards/earthquake-hazards",
				"https://earthquake.usgs.gov/learn/",
				"https://www.iris.edu/hq/",
				"https://www.britannica.com/science/seismology",
				"https://www.bgs.ac.uk/geological-topics/earthquakes-and-seismic-hazard/",
				"https://ds.iris.edu/ds/nodes/dmc/",
				"https://www.globalseismicnetwork.org/",
			)
		}

		// Metallurgy history / iron age / bronze age / smelting
		if containsAny(kwLower, []string{"metallurgy history", "iron age", "bronze age",
			"copper age", "smelting", "forge", "alloy history",
			"steel history", "cast iron", "wrought iron", "blast furnace",
			"steel making", "bessemer process", "crucible steel",
			"damascus steel", "sword making", "armor history",
			"mining history", "ore processing", "metal casting"}) {
			seeds = append(seeds,
				"https://www.worldhistory.org/Iron_Age/",
				"https://www.britannica.com/technology/metallurgy",
				"https://www.asminternational.org/",
				"https://www.smithsonianmag.com/category/history-archaeology/",
				"https://www.ancient.eu/metallurgy/",
				"https://www.sciencedirect.com/topics/materials-science/metallurgy",
				"https://archaeometallurgy.wordpress.com/",
			)
		}

		// Horology / clockmaking / timekeeping history
		if containsAny(kwLower, []string{"horology", "clockmaking", "timekeeping history",
			"sundial", "water clock", "clepsydra", "mechanical clock",
			"pendulum clock", "escapement", "spring driven clock",
			"pocket watch", "wristwatch history", "atomic clock",
			"longitude problem", "marine chronometer", "john harrison",
			"quartz clock", "gps time", "leap second"}) {
			seeds = append(seeds,
				"https://www.britishmuseum.org/collection/term/x35999",
				"https://www.britannica.com/technology/clock",
				"https://www.royalmintmuseum.org.uk/",
				"https://www.horologicalscience.org/",
				"https://www.nawcc.org/learn/horological-library/",
				"https://www.rigb.org/christmas-lectures/watch/",
				"https://collection.sciencemuseumgroup.org.uk/",
			)
		}

		// Printing history / Gutenberg / publishing / bookbinding
		if containsAny(kwLower, []string{"printing history", "gutenberg", "movable type", "woodblock print",
			"letterpress", "lithograph", "intaglio", "silk screen",
			"publishing history", "book history", "manuscript", "codex",
			"illuminated manuscript", "incunabula", "scriptorium",
			"paper making", "bookbinding", "newspaper history",
			"offset printing", "digital printing"}) {
			seeds = append(seeds,
				"https://www.gutenberg-museum.de/en/",
				"https://www.britannica.com/technology/printing-press",
				"https://www.bl.uk/history-of-writing/",
				"https://www.rarehistoricalbooks.com/",
				"https://www.loc.gov/exhibits/gutenberg/",
				"https://www.princetonol.com/groups/iad/lessons/high/printing.htm",
				"https://www.smithsonianmag.com/history/",
			)
		}

		// Coral reefs / kelp forests / marine ecology
		if containsAny(kwLower, []string{"coral reef", "kelp forest", "seagrass", "reef ecosystem",
			"bleaching coral", "reef fish", "symbiosis coral algae",
			"zooxanthellae", "coral spawning", "reef restoration",
			"ocean acidification reef", "great barrier reef",
			"intertidal zone", "rocky shore", "tidal pool",
			"mangrove ecosystem", "estuary ecology", "saltmarsh"}) {
			seeds = append(seeds,
				"https://coral.org/en/",
				"https://www.aims.gov.au/",
				"https://coralreef.noaa.gov/",
				"https://www.reefcheck.org/",
				"https://ocean.si.edu/ocean-life/invertebrates/coral-reefs",
				"https://www.nationalgeographic.com/environment/habitats/coral-reefs/",
				"https://www.seaworldentertainment.com/education/",
				"https://www.gbrmpa.gov.au/",
			)
		}

		// Desert ecology / arid environments / dryland
		if containsAny(kwLower, []string{"desert ecology", "arid environment", "dryland",
			"sahara desert", "gobi desert", "atacama desert", "mojave",
			"namib", "arabian desert", "sonoran desert",
			"desert adaptation", "xerophyte", "cactus", "succulent ecology",
			"desert animal", "camel adaptation", "fog desert",
			"dust storm", "desertification", "sand dune ecology"}) {
			seeds = append(seeds,
				"https://www.britannica.com/science/desert",
				"https://www.worldwildlife.org/biomes/deserts",
				"https://www.nationalgeographic.com/environment/habitats/deserts/",
				"https://desert.arizona.edu/",
				"https://www.desertmuseum.org/",
				"https://www.usgs.gov/special-topics/water-science-school/science/deserts",
				"https://www.canarianresearch.es/",
			)
		}

		// Wetlands / mangroves / peatlands / swamps
		if containsAny(kwLower, []string{"wetland", "mangrove", "peatland", "swamp", "bog",
			"fen", "marsh", "estuary", "floodplain", "riparian",
			"wetland ecology", "wetland bird", "wetland carbon",
			"ramsar wetland", "everglades", "pantanal", "okavango",
			"congo swamp", "peat carbon", "blanket bog", "sphagnum"}) {
			seeds = append(seeds,
				"https://www.ramsar.org/about-wetlands/",
				"https://www.wetlands.org/",
				"https://www.nationalgeographic.com/environment/habitats/wetlands/",
				"https://www.worldwildlife.org/habitats/freshwater",
				"https://www.epa.gov/wetlands/",
				"https://www.britannica.com/science/wetland",
				"https://www.iucn.org/resources/issues-briefs/peatlands-and-climate-change",
			)
		}

		// Fire ecology / wildfire science / prescribed burns
		if containsAny(kwLower, []string{"fire ecology", "wildfire", "prescribed burn",
			"fire adapted", "pyrophyte", "crown fire", "surface fire",
			"fire regime", "smoke ecology", "post fire recovery",
			"chaparral fire", "boreal fire", "savanna fire",
			"firefighting science", "fire behavior", "fire weather",
			"bark beetle wildfire", "carbon fire emissions"}) {
			seeds = append(seeds,
				"https://www.fs.usda.gov/science-technology/fire",
				"https://www.nwfirescience.org/",
				"https://www.fireecology.net/",
				"https://www.iawfonline.org/",
				"https://www.nifc.gov/",
				"https://www.britannica.com/science/fire-ecology",
				"https://www.fs.fed.us/fire/",
			)
		}

		// Evolutionary psychology / sociobiology / human nature
		if containsAny(kwLower, []string{"evolutionary psychology", "sociobiology", "human nature",
			"kin selection", "inclusive fitness", "reciprocal altruism",
			"sexual selection psychology", "mate choice", "parental investment",
			"status hierarchy", "coalition formation", "cheater detection",
			"fear psychology evolved", "disgust evolved",
			"eeo wilson", "richard dawkins", "selfish gene",
			"evolutionary psychiatry", "evolutionary medicine"}) {
			seeds = append(seeds,
				"https://plato.stanford.edu/entries/evolutionary-psychology/",
				"https://www.hbes.com/",
				"https://www.psychologytoday.com/us/basics/evolutionary-psychology",
				"https://www.britannica.com/science/sociobiology",
				"https://www.edge.org/",
				"https://www.epjournal.net/",
				"https://www.human-nature.com/",
			)
		}

		// Animal cognition / intelligence / tool use
		if containsAny(kwLower, []string{"animal cognition", "animal intelligence", "animal tool use",
			"crow intelligence", "dolphin cognition", "elephant memory",
			"octopus intelligence", "dog cognition", "parrot language",
			"mirror test", "theory of mind animal", "social learning animal",
			"problem solving animal", "numerical ability animal",
			"comparative psychology", "animal communication"}) {
			seeds = append(seeds,
				"https://www.cell.com/current-biology/collections/animal-cognition",
				"https://www.scientificamerican.com/article/animal-minds/",
				"https://www.britannica.com/science/animal-learning",
				"https://www.nature.com/subjects/animal-behaviour",
				"https://www.ted.com/topics/animals",
				"https://www.mpi.nl/departments/language-and-cognition/",
				"https://www.cognitivesciencesociety.org/",
			)
		}

		// Sports analytics / sabermetrics / performance data
		if containsAny(kwLower, []string{"sports analytics", "sabermetric", "moneyball",
			"expected goals", "war baseball", "wins above replacement",
			"on base percentage", "player tracking", "heat map sport",
			"performance analytics", "sports statistics", "xg soccer",
			"advanced metrics basketball", "passer rating", "analytics nba",
			"sports science performance", "biomechanics sport"}) {
			seeds = append(seeds,
				"https://www.baseballreference.com/",
				"https://fivethirtyeight.com/sports/",
				"https://statsbomb.com/articles/",
				"https://www.espn.com/nba/story/_/id/stats",
				"https://www.theathletic.com/",
				"https://www.tandfonline.com/toc/rjsp20/current",
				"https://www.sportsreference.com/",
				"https://www.americanfootballanalytics.com/",
			)
		}

		// Human-computer interaction / UX / usability
		if containsAny(kwLower, []string{"human computer interaction", "hci", "user experience",
			"usability", "user interface design", "interaction design",
			"cognitive ergonomics", "mental model", "affordance",
			"fitts law", "hick law", "gestalt ui", "accessibility",
			"user research", "usability testing", "eye tracking ui",
			"touch interface", "voice interface", "augmented reality ux"}) {
			seeds = append(seeds,
				"https://www.interaction-design.org/literature/",
				"https://www.nngroup.com/articles/",
				"https://www.usability.gov/",
				"https://chi.acm.org/",
				"https://www.smashingmagazine.com/category/ux-design/",
				"https://lawsofux.com/",
				"https://www.britannica.com/technology/human-computer-interaction",
				"https://www.hciresearch.org/",
			)
		}

		// Space propulsion / rocket engines / ion drives
		if containsAny(kwLower, []string{"rocket engine", "space propulsion", "ion drive",
			"chemical rocket", "liquid propellant", "solid rocket",
			"specific impulse", "thrust", "nozzle", "combustion chamber rocket",
			"ion thruster", "hall thruster", "electric propulsion",
			"nuclear propulsion", "solar sail", "laser propulsion",
			"staging rocket", "reusable rocket", "spacex engine", "raptor engine"}) {
			seeds = append(seeds,
				"https://www.nasa.gov/topics/technology/propulsion/",
				"https://www.grc.nasa.gov/www/k-12/rocket/",
				"https://www.aerojet.com/",
				"https://www.spacepropulsion.com/",
				"https://www.britannica.com/technology/rocket-and-missile-system",
				"https://www.iac.space/",
				"https://www.aiaa.org/propulsion",
				"https://ocw.mit.edu/courses/16-512-rocket-propulsion-fall-2005/",
			)
		}

		// Particle accelerators / LHC / detector physics
		if containsAny(kwLower, []string{"particle accelerator", "lhc", "large hadron collider",
			"cyclotron", "synchrotron", "linear accelerator", "linac",
			"particle detector", "cloud chamber", "bubble chamber",
			"cern", "fermilab", "slac", "higgs boson", "collider",
			"beamline", "synchrotron radiation", "free electron laser",
			"accelerator physics", "luminosity"}) {
			seeds = append(seeds,
				"https://home.cern/science/",
				"https://www.fnal.gov/pub/science/",
				"https://www.slac.stanford.edu/",
				"https://www.britannica.com/technology/particle-accelerator",
				"https://www.interactions.org/",
				"https://www.lhc-closer.es/",
				"https://particlephysicsnews.org/",
				"https://www.science.org/topic/category/physics",
			)
		}

		// Traditional medicine history — Ayurveda, TCM, Galenic
		if containsAny(kwLower, []string{"traditional medicine history", "ayurveda history",
			"traditional chinese medicine history", "galenic medicine",
			"humoral theory", "four humors", "hippocrates", "galen",
			"ibn sina avicenna", "herbalism history", "folk medicine",
			"medieval medicine", "ancient medicine egypt",
			"roman medicine", "greek medicine", "surgery history"}) {
			seeds = append(seeds,
				"https://www.britannica.com/science/history-of-medicine",
				"https://www.nlm.nih.gov/hmd/",
				"https://www.wellcomecollection.org/",
				"https://www.worldhistory.org/medicine/",
				"https://www.sciencemuseum.org.uk/objects-and-stories/medicine/",
				"https://www.rcpe.ac.uk/heritage/history-medicine",
				"https://himetop.wikidot.com/",
			)
		}

		// Plastics / polymers / synthetic materials history
		if containsAny(kwLower, []string{"plastic history", "polymer science", "synthetic material",
			"bakelite", "nylon history", "polyester", "polypropylene",
			"polyethylene", "pvc history", "rubber vulcanization",
			"polymer chemistry", "polymerization", "thermoplastic",
			"thermoset", "plastic pollution", "bioplastic",
			"recycling plastic", "cradle to grave"}) {
			seeds = append(seeds,
				"https://www.britannica.com/science/plastic",
				"https://www.americanchemistry.com/chemistry-in-america/chemistry-in-everyday-products/plastics",
				"https://www.sciencehistory.org/the-history-and-future-of-plastics",
				"https://www.plasticseurope.org/en/",
				"https://www.spe.org/resource-center/",
				"https://www.rsc.org/periodic-table/",
			)
		}

		// Urban ecology / cities as ecosystems / green infrastructure
		if containsAny(kwLower, []string{"urban ecology", "city ecosystem", "green infrastructure",
			"urban heat island", "urban biodiversity", "urban wildlife",
			"green roof", "urban forest", "urban stream", "impervious surface",
			"urban sustainability", "ecological footprint", "urban metabolism",
			"biophilic design", "urban resilience", "urban soil",
			"light pollution ecology", "noise pollution ecology"}) {
			seeds = append(seeds,
				"https://www.nwf.org/Garden-for-Wildlife/",
				"https://www.urbanlandscapelab.org/",
				"https://www.britannica.com/science/urban-ecology",
				"https://www.resilientcitiesnetwork.org/",
				"https://www.iclei.org/",
				"https://www.who.int/news-room/fact-sheets/detail/urban-green-spaces",
				"https://www.thenatureofcities.com/",
				"https://www.eea.europa.eu/themes/urban/",
			)
		}

		kw := *keyword
		for _, u := range seeds {
			addToQueue(u, 0, kw)
		}
		fmt.Printf("Seeded %d authoritative URLs for keyword: %s\n", len(seeds), *keyword)
	}

	// News mode exits after crawling — no web queue needed
	if newsHandled {
		return
	}

	// Load seed URLs if provided
	if *seedFile != "" {
		loadSeedFile(*seedFile)
	}

	// Initialize queue with seed URLs
	initQueue()

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < maxConcurrentWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			worker(workerID, *limit, *verbose)
		}(i)
	}
	wg.Wait()

	fmt.Println("Crawling completed!")
}

// initDB initializes the SQLite database
func initDB(dbPath string) {
	var err error
	// Pragmas matter here: without them concurrent crawler goroutines hit
	// SQLITE_BUSY and successfully-fetched pages were silently DISCARDED with
	// only a warning, losing crawl work. busy_timeout makes a writer wait for
	// the lock instead of failing, and WAL lets reads proceed during writes.
	dsn := dbPath + "?_pragma=busy_timeout(15000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err = sql.Open("sqlite", dsn)
	if err != nil {
		panic(fmt.Sprintf("Failed to open database: %v", err))
	}
	// SQLite permits a single writer. Serialising through one connection
	// removes lock contention entirely rather than relying on retry timing.
	db.SetMaxOpenConns(1)

	// Create tables if they don't exist.
	// Schema matches what upload_db.py expects: url/title/snippet in pages,
	// url/alt/source in images, url/filename/filetype/source in files.
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS pages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT UNIQUE,
			title TEXT,
			snippet TEXT,
			domain TEXT,
			path TEXT,
			content_hash TEXT,
			last_crawled TIMESTAMP,
			status_code INTEGER,
			response_time_ms INTEGER,
			content_type TEXT,
			content_length INTEGER,
			keywords TEXT
		);

		CREATE TABLE IF NOT EXISTS pages_content (
			page_id INTEGER PRIMARY KEY,
			content TEXT,
			FOREIGN KEY(page_id) REFERENCES pages(id)
		);

		CREATE TABLE IF NOT EXISTS images (
			url TEXT PRIMARY KEY,
			alt TEXT,
			source TEXT
		);

		CREATE TABLE IF NOT EXISTS files (
			url TEXT PRIMARY KEY,
			filename TEXT,
			filetype TEXT,
			source TEXT
		);

		CREATE TABLE IF NOT EXISTS crawl_queue (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT UNIQUE,
			depth INTEGER,
			keywords TEXT,
			added_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		panic(fmt.Sprintf("Failed to create tables: %v", err))
	}
}

// loadConfig loads crawler configuration from a file
func loadConfig(filename string) (map[string]interface{}, error) {
	config := make(map[string]interface{})

	// Check if file exists
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return config, fmt.Errorf("config file not found: %s", filename)
	}

	// Read the config file
	content, err := os.ReadFile(filename)
	if err != nil {
		return config, fmt.Errorf("error reading config file: %v", err)
	}

	// Parse INI-style config
	lines := strings.Split(string(content), "\n")
	currentSection := ""

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue // Skip empty lines and comments
		}

		// Check for section headers
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.Trim(line, "[]")
			config[currentSection] = make(map[string]interface{})
			continue
		}

		// Parse key-value pairs
		if strings.Contains(line, "=") {
			parts := strings.SplitN(line, "=", 2)
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])

			if currentSection != "" {
				// Add to section
				if section, ok := config[currentSection].(map[string]interface{}); ok {
					section[key] = value
				}
			} else {
				// Add to root
				config[key] = value
			}
		}
	}

	return config, nil
}

// initQueue initializes the crawl queue with seed URLs
func initQueue() {
	// Initialize queue with seed URLs based on the selected search type
	for _, url := range queue {
		addToQueue(url, 0, strings.Join(keywords, ","))
	}
}

// addToQueue adds a URL to the crawl queue if it hasn't been visited or queued already
func addToQueue(urlStr string, depth int, keywords string) {
	mu.Lock()
	defer mu.Unlock()

	// Skip if already visited or queued
	if visited[urlStr] || queued[urlStr] {
		return
	}

	// Skip blocked domains
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return
	}

	domain := parsedURL.Hostname()
	for _, blocked := range blockedDomains {
		if strings.Contains(domain, blocked) {
			return
		}
	}

	// Skip no-follow domains
	for _, noFollow := range noFollowDomains {
		if strings.Contains(domain, noFollow) {
			return
		}
	}

	// Skip if path contains blocked keywords
	lowerPath := strings.ToLower(parsedURL.Path)
	for _, blockedKeyword := range blockedPathKeywords {
		if strings.Contains(lowerPath, blockedKeyword) {
			return
		}
	}

	// Add to queue
	queue = append(queue, urlStr)
	queued[urlStr] = true

	// Also add to database queue
	_, err = db.Exec(
		"INSERT OR IGNORE INTO crawl_queue (url, depth, keywords) VALUES (?, ?, ?)",
		urlStr, depth, keywords,
	)
	if err != nil {
		fmt.Printf("Warning: Could not add URL to database queue: %v\n", err)
	}
}

// extractInfectionResearchData extracts specialized data for infection research
func extractInfectionResearchData(pageID int64, content, urlStr, contentType string) {
	// Create infection research data table if it doesn't exist
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS infection_research_data (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			page_id INTEGER,
			pathogen_type TEXT,
			pathogen_name TEXT,
			FOREIGN KEY(page_id) REFERENCES pages(id)
		)
	`)
	if err != nil {
		fmt.Printf("Warning: Could not create infection_research_data table: %v\n", err)
		return
	}

	// Extract basic infection research data
	pathogenType := ""
	pathogenName := ""
	lowerContent := strings.ToLower(content)

	// Determine pathogen type
	if strings.Contains(lowerContent, "virus") {
		pathogenType = "virus"
	} else if strings.Contains(lowerContent, "bacteria") {
		pathogenType = "bacteria"
	} else if strings.Contains(lowerContent, "fungus") {
		pathogenType = "fungus"
	} else if strings.Contains(lowerContent, "parasite") {
		pathogenType = "parasite"
	} else if strings.Contains(lowerContent, "prion") {
		pathogenType = "prion"
	}

	// Store the data
	_, err = db.Exec(
		"INSERT INTO infection_research_data (page_id, pathogen_type, pathogen_name) VALUES (?, ?, ?)",
		pageID, pathogenType, pathogenName,
	)
	if err != nil {
		fmt.Printf("Warning: Could not store infection research data: %v\n", err)
	}
}

// worker processes URLs from the queue
func worker(workerID, limit int, verbose bool) {
	for {
		mu.Lock()
		if len(queue) == 0 {
			mu.Unlock()
			break
		}
		if limit > 0 && len(visited) >= limit {
			mu.Unlock()
			break
		}
		urlStr := queue[0]
		queue = queue[1:]
		if visited[urlStr] {
			mu.Unlock()
			continue
		}
		visited[urlStr] = true
		searched[urlStr] = true
		mu.Unlock()

		if verbose {
			fmt.Printf("Worker %d: Processing %s\n", workerID, urlStr)
		}

		// Process the URL
		processURL(urlStr, verbose)
	}
}

// processURL crawls a single URL and extracts links
func processURL(urlStr string, verbose bool) {
	// Skip if URL is empty or invalid
	if urlStr == "" {
		return
	}

	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		fmt.Printf("Warning: Invalid URL %s: %v\n", urlStr, err)
		return
	}

	// Check robots.txt first
	if !checkRobotsTxt(urlStr) {
		if verbose {
			fmt.Printf("Skipping (robots.txt): %s\n", urlStr)
		}
		return
	}

	// Fetch the URL with retries
	var resp *http.Response
	var body []byte
	var finalURL string
	var statusCode int
	var contentType string
	var contentLength int64
	var responseTime time.Duration

	for retry := 0; retry < maxRetries; retry++ {
		startTime := time.Now()

		// Create HTTP client with timeout and redirect handling
		client := &http.Client{
			Timeout: requestTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= maxRedirects {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		}

		req, err := http.NewRequest("GET", urlStr, nil)
		if err != nil {
			fmt.Printf("Warning: Could not create request for %s: %v\n", urlStr, err)
			return
		}

		req.Header.Set("User-Agent", userAgent)
		resp, err = client.Do(req)

		responseTime = time.Since(startTime)

		if err != nil {
			if retry < maxRetries-1 {
				if verbose {
					fmt.Printf("Retry %d/%d for %s: %v\n", retry+1, maxRetries, urlStr, err)
				}
				time.Sleep(time.Duration(retry+1) * time.Second)
				continue
			}
			fmt.Printf("Warning: Could not fetch %s: %v\n", urlStr, err)
			return
		}
		defer resp.Body.Close()

		finalURL = resp.Request.URL.String()
		statusCode = resp.StatusCode
		contentType = resp.Header.Get("Content-Type")
		contentLength = resp.ContentLength

		// Read response body
		body, err = io.ReadAll(resp.Body)
		if err != nil {
			if retry < maxRetries-1 {
				if verbose {
					fmt.Printf("Retry %d/%d for %s (body read error): %v\n", retry+1, maxRetries, urlStr, err)
				}
				time.Sleep(time.Duration(retry+1) * time.Second)
				continue
			}
			fmt.Printf("Warning: Could not read body from %s: %v\n", urlStr, err)
			return
		}

		break // Success, exit retry loop
	}

	// Skip non-success status codes
	if statusCode < 200 || statusCode >= 400 {
		if verbose {
			fmt.Printf("Skipping (status %d): %s\n", statusCode, urlStr)
		}
		return
	}

	// Parse content based on type
	domain := parsedURL.Hostname()
	path := parsedURL.Path
	contentHash := fmt.Sprintf("%x", sha256.Sum256(body))

	// Extract keywords from URL and content
	extractedKeywords := extractKeywordsFromContent(string(body), urlStr)
	allKeywords := append(keywords, extractedKeywords...)
	keywordsStr := strings.Join(removeDuplicates(allKeywords), ",")

	// Store page in database
	pageID, err := storePage(finalURL, domain, path, contentHash, statusCode,
		responseTime.Milliseconds(), contentType, contentLength, keywordsStr)
	if err != nil {
		fmt.Printf("Warning: Could not store page %s: %v\n", urlStr, err)
		return
	}

	// Store content if it's HTML
	if strings.Contains(contentType, "text/html") {
		storePageContent(pageID, string(body))

		// Extract and store images
		extractAndStoreImages(pageID, string(body), finalURL)

		// Extract and store files/links
		extractAndStoreFiles(pageID, string(body), finalURL)

		// Extract links and add to queue
		extractLinksAndAddToQueue(string(body), finalURL, verbose)
	}

	// Specialized extraction for infection research
	if flag.Lookup("infection-research").Value.(flag.Getter).Get().(bool) {
		extractInfectionResearchData(pageID, string(body), finalURL, contentType)
	}

	if verbose {
		fmt.Printf("Saved [%s]: %s\n", domain, path)
	}
}

// loadSeedFile loads seed URLs from a JSON file
func loadSeedFile(filename string) {
	// This function would be implemented to load seed URLs
	// from a JSON file
}

// addInfectionResearchFlag adds a command line flag for infection research search
func addInfectionResearchFlag() {
	flag.Bool("infection-research", false, "Enable specialized infection, virus, and bacteria research search")
}

// handleInfectionResearchSearch handles the infection research specialized search
func handleInfectionResearchSearch() bool {
	infectionResearch := flag.Lookup("infection-research").Value.(flag.Getter).Get().(bool)
	if !infectionResearch {
		return false
	}

	fmt.Println("Starting specialized infection, virus, and bacteria research...")

	// Set up infection-related keywords
	infectionKeywords := []string{
		"infection", "virus", "bacteria", "pathogen", "microbe", "epidemic",
		"pandemic", "disease", "vaccine", "immunology", "antibiotics",
	}

	// Add configuration-enhanced keywords
	configFile := "infection_config.ini"
	if _, err := os.Stat(configFile); err == nil {
		config, err := loadConfig(configFile)
		if err == nil {
			if infectionConfig, ok := config["infection_research"].(map[string]interface{}); ok {
				// Add priority keywords from config
				if keywordsStr, ok := infectionConfig["priority_keywords"].(string); ok {
					for _, keyword := range strings.Split(keywordsStr, ",") {
						keyword = strings.TrimSpace(keyword)
						if keyword != "" {
							infectionKeywords = append(infectionKeywords, keyword)
						}
					}
				}

				// Add bioengineering keywords from config
				if bioKeywordsStr, ok := infectionConfig["bioengineering_keywords"].(string); ok {
					for _, keyword := range strings.Split(bioKeywordsStr, ",") {
						keyword = strings.TrimSpace(keyword)
						if keyword != "" {
							infectionKeywords = append(infectionKeywords, keyword)
						}
					}
				}
			}
		}
	}

	// Remove duplicates
	infectionKeywords = removeDuplicates(infectionKeywords)

	keywords = infectionKeywords
	seedKeywords = infectionKeywords
	allSeedKeywords = append(allSeedKeywords, infectionKeywords...)

	// Load infection-specific seed URLs
	loadInfectionSeedURLs()

	return true
}

// loadInfectionConfig loads configuration for infection research crawling
func loadInfectionConfig() {
	configFile := "infection_config.ini"
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		fmt.Printf("Warning: Infection config file not found: %s\n", configFile)
		return
	}

	// Load the config file
	config, err := loadConfig(configFile)
	if err != nil {
		fmt.Printf("Warning: Could not load infection config: %v\n", err)
		return
	}

	// Apply infection research settings
	if infectionConfig, ok := config["infection_research"].(map[string]interface{}); ok {
		// Set user agent if specified
		if userAgent, ok := infectionConfig["user_agent"].(string); ok {
			userAgent = userAgent
		}

		// Set delay if specified
		if delayStr, ok := infectionConfig["delay_ms"].(string); ok {
			delay, err := strconv.Atoi(delayStr)
			if err == nil {
				requestTimeout = time.Duration(delay) * time.Millisecond
			}
		}

		// Set priority keywords if specified
		if keywordsStr, ok := infectionConfig["priority_keywords"].(string); ok {
			priorityKeywords := strings.Split(keywordsStr, ",")
			for _, keyword := range priorityKeywords {
				keyword = strings.TrimSpace(keyword)
				if keyword != "" {
					seedKeywords = append(seedKeywords, keyword)
				}
			}
		}
	}

	// Load source authority information
	if sourceConfig, ok := config["source_authority"].(map[string]interface{}); ok {
		// Process government sources
		if govSources, ok := sourceConfig["government_sources"].(string); ok {
			for _, domain := range strings.Split(govSources, ",") {
				domain = strings.TrimSpace(domain)
				if domain != "" {
					_, err := db.Exec(
						"INSERT OR IGNORE INTO infection_sources (domain, is_government_source, authority_score) VALUES (?, 1, 100)",
						domain,
					)
					if err != nil {
						fmt.Printf("Warning: Could not store government source %s: %v\n", domain, err)
					}
				}
			}
		}
	}
}

// enhanceDatabaseSchema adds missing columns to existing databases (migration)
// and creates infection research specific tables.
func enhanceDatabaseSchema() {
	// If pages is an FTS5 virtual table (old schema), recreate it as a regular table.
	var pagesSQL string
	db.QueryRow("SELECT sql FROM sqlite_master WHERE name='pages'").Scan(&pagesSQL)
	if strings.Contains(pagesSQL, "fts5") || strings.Contains(pagesSQL, "VIRTUAL") {
		db.Exec(`ALTER TABLE pages RENAME TO pages_fts_old`)
		db.Exec(`CREATE TABLE pages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT UNIQUE,
			title TEXT,
			snippet TEXT,
			domain TEXT,
			path TEXT,
			content_hash TEXT,
			last_crawled TIMESTAMP,
			status_code INTEGER,
			response_time_ms INTEGER,
			content_type TEXT,
			content_length INTEGER,
			keywords TEXT
		)`)
		db.Exec(`INSERT OR IGNORE INTO pages (url, title, snippet) SELECT url, title, snippet FROM pages_fts_old`)
		db.Exec(`DROP TABLE pages_fts_old`)
		fmt.Println("Migrated pages table from FTS5 to regular schema.")
	}

	// Migration: add columns that older databases may be missing.
	// SQLite returns an error on duplicate column names — we intentionally ignore them.
	db.Exec(`ALTER TABLE pages ADD COLUMN snippet TEXT`)
	db.Exec(`ALTER TABLE pages ADD COLUMN domain TEXT`)
	db.Exec(`ALTER TABLE pages ADD COLUMN path TEXT`)
	db.Exec(`ALTER TABLE pages ADD COLUMN content_hash TEXT`)
	db.Exec(`ALTER TABLE pages ADD COLUMN last_crawled TIMESTAMP`)
	db.Exec(`ALTER TABLE pages ADD COLUMN status_code INTEGER`)
	db.Exec(`ALTER TABLE pages ADD COLUMN response_time_ms INTEGER`)
	db.Exec(`ALTER TABLE pages ADD COLUMN content_type TEXT`)
	db.Exec(`ALTER TABLE pages ADD COLUMN content_length INTEGER`)
	db.Exec(`ALTER TABLE pages ADD COLUMN keywords TEXT`)

	// crawl_queue may be missing depth/keywords columns
	db.Exec(`ALTER TABLE crawl_queue ADD COLUMN depth INTEGER DEFAULT 0`)
	db.Exec(`ALTER TABLE crawl_queue ADD COLUMN keywords TEXT`)
	db.Exec(`ALTER TABLE crawl_queue ADD COLUMN added_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP`)

	// Older image schema used alt_text + page_id instead of alt + source.
	db.Exec(`ALTER TABLE images ADD COLUMN alt TEXT`)
	db.Exec(`ALTER TABLE images ADD COLUMN source TEXT`)
	db.Exec(`UPDATE images SET alt = alt_text WHERE alt IS NULL AND alt_text IS NOT NULL`)

	// Older files schema used file_type + page_id instead of filename/filetype/source.
	db.Exec(`ALTER TABLE files ADD COLUMN filename TEXT`)
	db.Exec(`ALTER TABLE files ADD COLUMN filetype TEXT`)
	db.Exec(`ALTER TABLE files ADD COLUMN source TEXT`)
	db.Exec(`UPDATE files SET filetype = file_type WHERE filetype IS NULL AND file_type IS NOT NULL`)

	// Create infection research specific tables
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS infection_research_data (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			page_id INTEGER,
			pathogen_type TEXT,
			pathogen_name TEXT,
			disease_name TEXT,
			research_focus TEXT,
			bioengineering_approach TEXT,
			key_findings TEXT,
			publication_date TEXT,
			authors TEXT,
			institution TEXT,
			journal TEXT,
			doi TEXT,
			methods_used TEXT,
			clinical_relevance TEXT,
			data_quality TEXT,
			last_updated TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(page_id) REFERENCES pages(id)
		);

		CREATE TABLE IF NOT EXISTS infection_research_metadata (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			page_id INTEGER,
			genetic_sequence_available BOOLEAN DEFAULT 0,
			protein_structure_available BOOLEAN DEFAULT 0,
			experimental_data_available BOOLEAN DEFAULT 0,
			clinical_trial_data_available BOOLEAN DEFAULT 0,
			has_bioengineering_content BOOLEAN DEFAULT 0,
			has_ai_ml_content BOOLEAN DEFAULT 0,
			has_nanotechnology_content BOOLEAN DEFAULT 0,
			has_synthetic_biology_content BOOLEAN DEFAULT 0,
			data_completeness_score INTEGER DEFAULT 0,
			data_reliability_score INTEGER DEFAULT 0,
			FOREIGN KEY(page_id) REFERENCES pages(id)
		);

		CREATE TABLE IF NOT EXISTS infection_sources (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain TEXT UNIQUE,
			authority_score INTEGER DEFAULT 0,
			is_government_source BOOLEAN DEFAULT 0,
			is_academic_source BOOLEAN DEFAULT 0,
			is_health_organization BOOLEAN DEFAULT 0,
			is_bioengineering_source BOOLEAN DEFAULT 0,
			is_scientific_journal BOOLEAN DEFAULT 0,
			last_updated TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		fmt.Printf("Warning: Could not create infection research tables: %v\n", err)
	}
}

// loadInfectionSeedURLs loads seed URLs specifically for infection research
func loadInfectionSeedURLs() {
	// Load infection-specific seed URLs from JSON file
	seedFile := "infection_seed_urls.json"
	if _, err := os.Stat(seedFile); os.IsNotExist(err) {
		fmt.Printf("Warning: Infection seed file not found: %s\n", seedFile)
		return
	}

	// Read the seed file
content, err := os.ReadFile(seedFile)
if err != nil {
	fmt.Printf("Warning: Could not read infection seed file: %v\n", err)
	return
}

// Parse JSON
var seedData map[string]interface{}
err = json.Unmarshal(content, &seedData)
if err != nil {
	fmt.Printf("Warning: Could not parse infection seed file: %v\n", err)
	return
}

// Extract infection research data
if infectionData, ok := seedData["infection_research"].(map[string]interface{}); ok {
	// Add authoritative sources to queue
	if sources, ok := infectionData["authoritative_sources"].([]interface{}); ok {
		for _, source := range sources {
			if url, ok := source.(string); ok {
				addToQueue(url, 0, strings.Join(keywords, ","))
			}
		}
	}

	// Add search engine queries for each keyword
	if searchEngines, ok := infectionData["search_engines"].([]interface{}); ok {
		for _, engine := range searchEngines {
			if engineURL, ok := engine.(string); ok {
				for _, keyword := range keywords {
					searchURL := fmt.Sprintf(engineURL, url.QueryEscape(keyword))
					addToQueue(searchURL, 0, keyword)
				}
			}
		}
	}

	// Add specialized databases
	if databases, ok := infectionData["specialized_databases"].([]interface{}); ok {
		for _, db := range databases {
			if dbMap, ok := db.(map[string]interface{}); ok {
				if url, ok := dbMap["url"].(string); ok {
					addToQueue(url, 0, "specialized database")
				}
			}
		}
	}

	// Add bioengineering resources
	if bioResources, ok := infectionData["bioengineering_resources"].([]interface{}); ok {
		for _, resource := range bioResources {
			if resMap, ok := resource.(map[string]interface{}); ok {
				if url, ok := resMap["url"].(string); ok {
					addToQueue(url, 0, "bioengineering resource")
				}
			}
		}
	}
}

fmt.Printf("Loaded %d infection research seed URLs\n", len(queue))
}

// Include the specialized search functions from other files
// These are included via the go:generate directive or by importing