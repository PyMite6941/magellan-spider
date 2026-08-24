#!/bin/bash
# crawl_all.sh — Run the spider across all topic categories.
# Usage: bash crawl_all.sh
# Each keyword triggers the matching seed category in main.go.

set -e
SPIDER="./magellan-spider.exe"
LIMIT=500  # pages per topic — tune up/down as needed

if [ ! -f "$SPIDER" ]; then
    echo "Building spider..."
    go build -o magellan-spider.exe .
fi

run() {
    local kw="$1"
    echo ""
    echo "=========================================="
    echo "  Crawling: $kw"
    echo "=========================================="
    "$SPIDER" -keyword "$kw" -limit $LIMIT
}

# ── Programming & Computer Science ────────────────────────────────────────────
run "python programming language"
run "javascript web development"
run "rust programming language"
run "golang programming language"
run "linux operating system"
run "algorithms and data structures"
run "database systems SQL"
run "compiler design"
run "open source software"
run "computer networking"
run "operating system concepts"
run "software engineering principles"

# ── Mathematics ───────────────────────────────────────────────────────────────
run "calculus differential integral"
run "linear algebra vectors matrices"
run "statistics probability theory"
run "number theory prime numbers"
run "topology manifolds"
run "abstract algebra group theory"
run "real analysis measure theory"
run "discrete mathematics combinatorics"
run "graph theory"
run "fourier analysis signal processing"
run "numerical methods computation"
run "bayesian statistics inference"

# ── Physics ───────────────────────────────────────────────────────────────────
run "classical mechanics physics"
run "quantum mechanics physics"
run "electromagnetism physics"
run "thermodynamics statistical mechanics"
run "special relativity general relativity"
run "nuclear physics"
run "particle physics standard model"
run "condensed matter physics"
run "optics light waves"
run "fluid mechanics"

# ── Chemistry ─────────────────────────────────────────────────────────────────
run "organic chemistry"
run "inorganic chemistry"
run "physical chemistry"
run "biochemistry molecular biology"
run "analytical chemistry"
run "polymer chemistry materials"
run "electrochemistry"
run "spectroscopy chemistry"

# ── Biology & Life Sciences ───────────────────────────────────────────────────
run "cell biology molecular biology"
run "genetics genomics"
run "evolution natural selection"
run "ecology ecosystems"
run "neuroscience brain"
run "microbiology bacteria viruses"
run "immunology vaccines"
run "botany plant biology"
run "zoology animal behavior ethology"
run "marine biology ocean life"
run "entomology insects"
run "ornithology birds"
run "paleontology fossils"

# ── Medicine & Health ─────────────────────────────────────────────────────────
run "human anatomy physiology"
run "cardiology heart disease"
run "neurology neurological disorders"
run "oncology cancer biology"
run "pharmacology drug mechanisms"
run "surgery medical procedures"
run "epidemiology public health"
run "nutrition dietary science"
run "sports medicine exercise physiology"
run "psychiatry mental health"
run "pediatrics child development"
run "dermatology skin"

# ── Earth & Space Sciences ────────────────────────────────────────────────────
run "geology tectonic plates"
run "volcanology earthquakes"
run "mineralogy rocks gems"
run "oceanography marine science"
run "meteorology weather atmosphere"
run "climate science global warming"
run "hydrology rivers watersheds"
run "astronomy astrophysics cosmology"
run "solar system planets"
run "black holes neutron stars supernovae"
run "dark matter dark energy"
run "space exploration nasa"

# ── Engineering ───────────────────────────────────────────────────────────────
run "civil engineering structures bridges"
run "mechanical engineering thermodynamics"
run "electrical engineering circuits"
run "aerospace engineering aerodynamics"
run "chemical engineering processes"
run "materials science"
run "robotics control systems"
run "signal processing"

# ── History ───────────────────────────────────────────────────────────────────
run "ancient greece history"
run "ancient rome history"
run "ancient egypt history"
run "ancient mesopotamia history"
run "medieval history europe"
run "byzantine empire history"
run "renaissance history"
run "age of exploration history"
run "french revolution history"
run "industrial revolution history"
run "world war one history"
run "world war two history"
run "cold war history"
run "ancient china history"
run "ancient india history"
run "ottoman empire history"
run "mongol empire history"
run "viking history"
run "american history colonial"
run "african history civilizations"
run "mesoamerican civilizations aztec maya"

# ── Military History ──────────────────────────────────────────────────────────
run "ancient warfare tactics"
run "medieval warfare siege"
run "naval history battles"
run "military technology weapons history"
run "napoleon warfare strategy"
run "world war tactics strategy"

# ── Geography & Earth ─────────────────────────────────────────────────────────
run "physical geography cartography"
run "geology geomorphology"
run "biomes ecosystems world"
run "world rivers mountains"
run "country cultures geography"

# ── Linguistics & Languages ───────────────────────────────────────────────────
run "etymology word origins"
run "historical linguistics"
run "phonetics phonology grammar"
run "writing systems scripts"
run "endangered languages"
run "indo-european language family"
run "language acquisition linguistics"

# ── Philosophy ────────────────────────────────────────────────────────────────
run "philosophy ethics moral theory"
run "epistemology knowledge truth"
run "metaphysics ontology"
run "philosophy of mind consciousness"
run "political philosophy"
run "logic formal reasoning"
run "philosophy of science"
run "ancient greek philosophy"
run "existentialism philosophy"

# ── Religion & Mythology ──────────────────────────────────────────────────────
run "greek mythology gods heroes"
run "norse mythology viking gods"
run "roman mythology"
run "egyptian mythology gods"
run "hinduism theology texts"
run "buddhism philosophy meditation"
run "islam history theology"
run "christianity history theology"
run "judaism history theology"
run "world mythology comparative"
run "shamanism ancient religion"

# ── Psychology & Cognitive Science ────────────────────────────────────────────
run "cognitive psychology memory learning"
run "social psychology behavior"
run "developmental psychology"
run "abnormal psychology disorders"
run "neuroscience cognitive science"
run "behavioral economics decision making"
run "personality psychology"

# ── Economics & Social Sciences ───────────────────────────────────────────────
run "microeconomics supply demand"
run "macroeconomics GDP inflation"
run "behavioral economics"
run "game theory economics"
run "sociology social structures"
run "anthropology cultures"
run "political science government"

# ── Law ───────────────────────────────────────────────────────────────────────
run "constitutional law history"
run "common law jurisprudence"
run "criminal law"
run "international law"
run "intellectual property copyright patent"

# ── Arts & Culture ────────────────────────────────────────────────────────────
run "art history renaissance baroque"
run "impressionism modern art"
run "sculpture ancient modern"
run "architecture history"
run "photography history techniques"
run "film cinema history"
run "literature poetry analysis"
run "theater drama history"
run "dance history forms"

# ── Music ─────────────────────────────────────────────────────────────────────
run "music theory harmony counterpoint"
run "classical music baroque composers"
run "romantic music composers"
run "jazz music history theory"
run "folk music world traditions"
run "musical instruments"
run "music composition orchestration"

# ── Sports & Athletics ────────────────────────────────────────────────────────
run "olympic games history sports"
run "soccer football history tactics"
run "basketball history tactics"
run "baseball history statistics"
run "tennis history tactics"
run "swimming athletics track field"
run "martial arts history techniques"
run "cycling racing history"
run "exercise physiology fitness"

# ── Food & Cuisine ────────────────────────────────────────────────────────────
run "food science fermentation"
run "italian cuisine history"
run "french cuisine culinary arts"
run "japanese food culture"
run "chinese cuisine regional"
run "indian food spices cuisine"
run "baking pastry bread"
run "wine beer brewing history"
run "food history ancient culinary"

# ── Hobbies & Crafts ──────────────────────────────────────────────────────────
run "railroad history trains"
run "model railroading hobby"
run "woodworking joinery"
run "3d printing additive manufacturing"
run "amateur radio electronics"
run "beekeeping apiculture"
run "aquarium fishkeeping"
run "origami paper folding"
run "coin collecting numismatics"
run "stamp collecting philately"
run "chess history strategy"
run "birdwatching ornithology hobby"

# ── Automotive & Aviation ─────────────────────────────────────────────────────
run "automobile history engineering"
run "formula 1 racing"
run "electric vehicles technology"
run "aviation aircraft history"
run "aerospace history rockets"
run "motorcycle history engineering"

# ── Statistics & Probability (applied) ───────────────────────────────────────
run "poker mathematics probability"
run "casino games odds house edge"
run "monte carlo simulation"
run "regression analysis statistics"
run "machine learning statistics"
run "data science statistics"

# ── Advanced Materials & Atmospheric Water Harvesting ─────────────────────────
run "metal organic framework MOF porous"
run "covalent organic framework COF reticular chemistry"
run "porous organic polymer POP hypercrosslinked"
run "hydrogel atmospheric water harvesting AWG"
run "aerogel silica carbon thermal insulation"
run "bio-inspired water collection Namib beetle fog harvesting"
run "bioinspired surface wettability superhydrophobic"
run "programmable nanomaterials stimuli-responsive shape memory"
run "bio-inspired programmable materials soft matter actuator"
run "4D printing shape morphing liquid crystal elastomer"
run "atmospheric water generation desiccant sorbent"
run "reticular chemistry MOF COF design synthesis"

# --- Education and academic topics ---
# Magellan is a STEAM/academic search engine, but the topic list covered
# subjects without covering education itself: no pedagogy, curriculum, open
# educational resources or scholarly-publishing topics. These pair with the
# education seeds added to starter_websites.json.
run "open educational resources OER"
run "STEM education curriculum"
run "STEAM education arts integration"
run "pedagogy teaching methods"
run "learning sciences cognition education"
run "educational technology edtech"
run "distance learning online education"
run "curriculum design instructional design"
run "assessment evaluation education"
run "early childhood education development"
run "higher education university research"
run "vocational technical education training"
run "special education inclusive learning"
run "literacy numeracy education"
run "teacher training professional development"
run "education policy reform"
run "educational psychology motivation"
run "open access scholarly publishing"
run "academic research methods methodology"
run "digital libraries archives education"
run "MOOC massive open online course"
run "science communication outreach education"
run "global education development access"
run "homeschooling alternative education"
run "computer science education programming pedagogy"

echo ""
echo "=========================================="
echo "  All topics complete!"
echo "  Run: bash finish_setup.sh   to upload"
echo "=========================================="
