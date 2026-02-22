package text

import (
	"regexp"
	"strings"
)

type INormalizer interface {
	Normalize(text string) string
}

// Normalizer struct holds the fluff map for O(1) lookups
type Normalizer struct {
	fluffMap map[string]struct{}
}

type Language string

const (
	ENGLISH           Language = "en"
	SPANISH           Language = "es"
	FRENCH            Language = "fr"
	GERMAN            Language = "de"
	JAPANESE          Language = "ja"
	CHINESE           Language = "zh"
	HINDI             Language = "hi"
	BENGALI           Language = "bn"
	TAMIL             Language = "ta"
	PUNJABI           Language = "pa"    // Gurmukhi (primarily India)
	PUNJABI_SHAHMUKHI Language = "pa_ps" // Shahmukhi (primarily Pakistan)
	RUSSIAN           Language = "ru"
	ARABIC            Language = "ar"
	PORTUGUESE        Language = "pt"
	MARATHI           Language = "mr"
	GUJARATI          Language = "gu"
)

var Fluff = map[Language][]string{
	"en": {
		// --- 1. CONVERSATIONAL FILLERS, SOUNDS & HEDGES ---
		"uh", "um", "ah", "er", "oh", "hm", "hmm", "hmmm", "huh", "eh", "meh", "duh",
		"uh-huh", "mhm", "aha", "oops", "whoops", "wow", "whoa", "yikes", "gosh", "gee",
		"well", "like", "so", "right", "okay", "ok", "k", "yep", "yup", "yeah", "yea",
		"nope", "nah", "aye", "nay", "alright", "aw", "shucks", "actually", "basically",
		"literally", "totally", "seriously", "honestly", "frankly", "truly", "simply",
		"merely", "just", "only", "really", "very", "quite", "mostly", "mainly",
		"virtually", "essentially", "fundamentally", "supposedly", "presumably",
		"arguably", "apparently", "evidently", "obviously", "clearly", "naturally",
		"kind", "sort", "type", "mean", "guess", "suppose", "reckon", "figure", "say",
		"wonder", "stuff", "thing", "things", "yada", "blah", "etc", "et", "cetera",
		"anyway", "anyways", "anyhow", "however", "moreover", "furthermore", "anywho",

		// --- 2. PRONOUNS (COMPLETE REMOVAL) ---
		"i", "me", "my", "mine", "myself", "we", "us", "our", "ours", "ourselves",
		"you", "your", "yours", "yourself", "yourselves", "he", "him", "his", "himself",
		"she", "her", "hers", "herself", "it", "its", "itself", "they", "them", "their",
		"theirs", "themselves", "one", "ones", "oneself", "who", "whom", "whose",
		"which", "what", "whatever", "whatsoever", "whichever", "whoever", "whomever",
		"this", "that", "these", "those", "anyone", "anybody", "anything", "everyone",
		"everybody", "everything", "someone", "somebody", "something", "noone",
		"nobody", "nothing", "none", "neither", "either", "each", "every", "all", "both",
		"some", "any", "another", "other", "others", "such",

		// --- 3. ARTICLES, PREPOSITIONS & CONJUNCTIONS ---
		"a", "an", "the", "and", "or", "but", "nor", "for", "yet", "so", "as", "if",
		"of", "at", "by", "to", "in", "on", "up", "off", "out", "with", "into", "unto",
		"from", "over", "under", "below", "above", "down", "through", "thru", "via",
		"across", "along", "amid", "amidst", "among", "amongst", "around", "round",
		"about", "against", "after", "before", "behind", "beneath", "beside", "besides",
		"between", "betwixt", "beyond", "during", "inside", "outside", "within", "without",
		"upon", "towards", "toward", "past", "since", "until", "till", "despite",
		"except", "save", "per", "plus", "minus", "versus", "vs", "via", "than", "then",
		"once", "while", "when", "where", "why", "how", "because", "although", "though",
		"unless", "whereas", "whether", "while", "whilst", "lest", "provided", "providing",
		"assuming", "supposing", "considering", "regarding", "respecting", "concerning",

		// --- 4. AUXILIARY VERBS & MODALS (ALL TENSES) ---
		"am", "is", "are", "was", "were", "be", "been", "being", "have", "has", "had",
		"having", "do", "does", "did", "doing", "done", "can", "could", "shall", "should",
		"will", "would", "may", "might", "must", "ought", "need", "dare", "used",
		"get", "gets", "getting", "got", "gotten", "become", "becomes", "becoming",
		"became", "seem", "seems", "seeming", "seemed", "appear", "appears", "appearing",
		"appeared", "look", "looks", "looking", "looked", "sound", "sounds", "sounding",
		"sounded", "feel", "feels", "feeling", "felt", "go", "goes", "going", "went",
		"gone", "come", "comes", "coming", "came", "make", "makes", "making", "made",
		"take", "takes", "taking", "took", "taken", "put", "puts", "putting", "give",
		"gives", "giving", "given", "gave", "let", "lets", "letting", "want", "wants",
		"wanting", "wanted", "wish", "wishes", "wishing", "wished", "hope", "hopes",
		"hoping", "hoped", "try", "tries", "trying", "tried",

		// --- 5. TEMPORAL & FREQUENCY (TIME NOISE) ---
		"now", "then", "today", "tonight", "soon", "later",
		"early", "late", "already", "yet", "still", "ago", "before", "after", "recently",
		"lately", "currently", "presently", "instantly", "immediately", "directly",
		"shortly", "eventually", "finally", "ultimately", "always", "ever", "never",
		"often", "frequently", "usually", "generally", "normally", "typically", "commonly",
		"rarely", "seldom", "occasionally", "sometimes", "daily", "weekly", "monthly",
		"yearly", "annually", "morning", "noon", "afternoon", "evening", "night",
		"midnight", "hour", "minute", "second", "moment", "time", "times", "day", "days",
		"week", "weeks", "month", "months", "year", "years", "decades", "centuries",
		"past", "future", "present", "meanwhile", "meantime", "subsequently", "prior",
		"previous", "next", "last", "following", "preceding", "forthcoming", "upcoming",
		"whenever", "wherever", "forever", "nowadays", "anymore",

		// --- 6. ADVERBS OF DEGREE & MANNER ---
		"very", "really", "quite", "too", "so", "much", "many", "more", "most", "less",
		"least", "little", "bit", "lot", "lots", "plenty", "bunch", "ton", "heap",
		"extremely", "incredibly", "awfully", "terribly", "fairly", "pretty", "rather",
		"somewhat", "slightly", "barely", "hardly", "scarcely", "nearly", "almost",
		"approximately", "about", "around", "roughly", "exactly", "precisely", "just",
		"only", "simply", "merely", "purely", "totally", "completely", "absolutely",
		"entirely", "wholly", "fully", "utterly", "thoroughly", "perfectly", "hilly",
		"badly", "poorly", "well", "nicely", "finely", "greatly", "strongly", "weakly",
		"lightly", "heavily", "slowly", "quickly", "rapidly", "fast", "swiftly",
		"loudly", "quietly", "softly", "hard", "easy", "easily", "smoothly", "rough",
		"roughly", "surely", "certainly", "definitely", "probably", "possibly", "maybe",
		"perhaps", "likely", "unlikely", "luckily", "fortunately", "unfortunately",
		"regrettably", "thankfully", "hopefully", "sadly", "happily", "gladly",

		// --- 7. SOCIAL, POLITENESS & GREETINGS ---
		"hello", "hi", "hey", "yo", "sup", "greetings", "goodbye", "bye", "ciao",
		"farewell", "please", "pls", "plz", "thanks", "thx", "thank", "thankyou",
		"gratitude", "welcome", "sorry", "apologies", "pardon", "excuse", "forgive",
		"regret", "apologize", "good", "morning", "afternoon", "evening", "night",
		"great", "fine", "okay", "excellent", "wonderful", "cool", "awesome", "nice",
		"fantastic", "perfect", "brilliant", "lovely", "beautiful", "dear", "kind",
		"kindly", "sir", "madam", "ma'am", "miss", "mister", "mr", "mrs", "ms", "dr",
		"buddy", "pal", "mate", "dude", "friend", "guy", "guys", "man", "woman", "boy",
		"girl", "gentlemen", "ladies", "folks", "everyone", "everybody", "people",

		// --- 8. VAGUE NOUNS & QUANTIFIERS ---
		"thing", "things", "stuff", "junk", "matter", "matters", "object", "objects",
		"item", "items", "piece", "pieces", "part", "parts", "bit", "bits", "chunk",
		"chunks", "slice", "slices", "portion", "portions", "section", "sections",
		"segment", "segments", "amount", "amounts", "quantity", "quantities", "number",
		"numbers", "level", "levels", "degree", "degrees", "extent", "extents", "way",
		"ways", "means", "method", "methods", "mode", "modes", "style", "styles",
		"type", "types", "kind", "kinds", "sort", "sorts", "variety", "varieties",
		"class", "classes", "category", "categories", "group", "groups", "set", "sets",
		"list", "lists", "collection", "collections", "batch", "batches", "series",
		"bunch", "pile", "stack", "load", "heap", "mass", "couple", "pair", "few",
		"several", "various", "numerous", "countless", "multiple", "single", "double",
		"triple", "half", "quarter", "third", "whole", "total", "entire", "full",
		"empty", "some", "any", "no", "all", "none", "rest", "remainder", "other",
		"others", "another", "else", "difference", "same", "similar", "different",

		// --- 9. WEB/UI COMMAND PADDING ---
		// Words often used *around* the actual intent in tech contexts
		"click", "press", "tap", "hit", "select", "choose", "check", "uncheck", "box",
		"button", "link", "icon", "tab", "menu", "option", "options", "setting",
		"settings", "preference", "preferences", "config", "configuration", "screen",
		"page", "site", "website", "web", "view", "show", "hide", "display", "open",
		"close", "start", "stop", "begin", "end", "finish", "cancel", "abort",
		"continue", "proceed", "go", "back", "forward", "next", "previous", "submit",
		"enter", "input", "output", "result", "results", "info", "information", "data",
		"detail", "details", "description", "content", "text", "message", "note",
		"status", "state", "mode", "feature", "function", "action", "activity", "task",
		"job", "process", "operation", "system", "app", "application", "program",
		"software", "tool", "utility", "device", "machine", "hardware", "user",
		"account", "profile", "login", "log", "sign", "access", "permission", "auth",

		// --- 10. CONTRACTIONS (SPLIT & FULL) ---
		"aren't", "can't", "couldn't", "didn't", "doesn't", "don't", "hadn't", "hasn't",
		"haven't", "he'd", "he'll", "he's", "i'd", "i'll", "i'm", "i've", "isn't",
		"it'd", "it'll", "it's", "let's", "mightn't", "mustn't", "shan't", "she'd",
		"she'll", "she's", "shouldn't", "that's", "there's", "they'd", "they'll",
		"they're", "they've", "wasn't", "we'd", "we'll", "we're", "we've", "weren't",
		"what'll", "what're", "what's", "what've", "where's", "who'd", "who'll",
		"who're", "who's", "who've", "won't", "wouldn't", "you'd", "you'll", "you're",
		"you've", "ain't", "gonna", "wanna", "gotta", "imma", "finna", "tryna",

		// --- 11. LOCATION & DIRECTION NOISE ---
		"here", "there", "where", "everywhere", "anywhere", "nowhere", "somewhere",
		"hither", "thither", "whither", "yonder", "nearby", "near", "far", "away",
		"close", "distant", "local", "global", "internal", "external", "interior",
		"exterior", "upper", "lower", "middle", "center", "centre", "top", "bottom",
		"side", "front", "back", "rear", "left", "right", "north", "south", "east",
		"west", "northern", "southern", "eastern", "western", "upward", "downward",
		"forward", "backward", "sideways", "inward", "outward", "homeward", "upstairs",
		"downstairs", "indoors", "outdoors", "inside", "outside",

		// --- 12. GENERIC DESCRIPTIVE ADJECTIVES (Subjective/Low-Info) ---
		"big", "large", "huge", "giant", "small", "tiny", "little", "short", "long",
		"tall", "wide", "narrow", "thick", "thin", "heavy", "light", "high", "low",
		"fast", "slow", "quick", "rapid", "swift", "hot", "cold", "warm", "cool",
		"good", "bad", "great", "terrible", "awful", "horrible", "fine", "nice",
		"pleasant", "lovely", "beautiful", "ugly", "pretty", "best", "better", "worst",
		"worse", "easy", "hard", "difficult", "simple", "complex", "complicated",
		"clear", "vague", "certain", "uncertain", "sure", "unsure", "real", "fake",
		"true", "false", "right", "wrong", "correct", "incorrect", "new", "old",
		"young", "fresh", "stale", "clean", "dirty", "empty", "full", "open", "closed",
		"busy", "free", "available", "unavailable", "ready", "set", "safe", "dangerous",
		"important", "significant", "major", "minor", "key", "main", "primary",
		"secondary", "basic", "advanced", "standard", "normal", "regular", "usual",
		"strange", "odd", "weird", "unusual", "special", "specific", "general",
		"public", "private", "personal", "official", "formal", "casual",
	},
	"es": { // Spanish
		// Articles & Demonstratives
		"el", "la", "los", "las", "un", "una", "unos", "unas", "lo", "al", "del",
		"este", "esta", "estos", "estas", "esto", "ese", "esa", "esos", "esas", "eso",
		"aquel", "aquella", "aquellos", "aquellas", "aquello",
		// Pronouns
		"yo", "tú", "tu", "él", "ella", "ello", "nosotros", "nosotras", "vosotros", "vosotras",
		"ellos", "ellas", "usted", "ustedes", "mí", "ti", "sí", "conmigo", "contigo", "consigo",
		"me", "te", "se", "nos", "os", "le", "les", "mi", "mis", "tu", "tus", "su", "sus",
		"nuestro", "nuestra", "nuestros", "nuestras", "vuestro", "vuestra", "vuestros", "vuestras",
		"mío", "mía", "míos", "mías", "tuyo", "tuya", "tuyos", "tuyas", "suyo", "suya", "suyos", "suyas",
		// Prepositions & Conjunctions
		"a", "ante", "bajo", "cabe", "con", "contra", "de", "desde", "durante", "en", "entre",
		"hacia", "hasta", "mediante", "para", "por", "según", "sin", "so", "sobre", "tras",
		"versus", "vía", "y", "e", "ni", "o", "u", "pero", "mas", "sino", "aunque", "porque",
		"pues", "ya", "que", "si", "como", "cuando", "donde", "mientras", "apenas",
		// Verbs (Ser/Estar/Haber/Tener/Hacer/Ir - Common conjugations)
		"soy", "eres", "es", "somos", "sois", "son", "fui", "fuiste", "fue", "fuimos", "fuisteis", "fueron",
		"era", "eras", "éramos", "erais", "eran", "seré", "serás", "será", "seremos", "seréis", "serán",
		"sido", "siendo", "estoy", "estás", "está", "estamos", "estáis", "están", "estuve", "estuviste",
		"estuvo", "estuvimos", "estuvisteis", "estuvieron", "estaba", "estabas", "estábamos", "estabais", "estaban",
		"he", "has", "ha", "hemos", "habéis", "han", "había", "habías", "habíamos", "habíais", "habían",
		"hube", "hubiste", "hubo", "hubimos", "hubisteis", "hubieron", "habré", "habrás", "habrá",
		"tengo", "tienes", "tiene", "tenemos", "tenéis", "tienen", "tuve", "tuviste", "tuvo", "tuvimos",
		"hago", "haces", "hace", "hacemos", "hacéis", "hacen", "hice", "hiciste", "hizo", "hicimos",
		"voy", "vas", "va", "vamos", "vais", "van", "iba", "ibas", "íbamos", "ibais", "iban",
		// Adverbs & Fillers
		"aquí", "ahí", "allí", "allá", "acá", "cerca", "lejos", "hoy", "ayer", "mañana",
		"ahora", "luego", "después", "antes", "pronto", "tarde", "siempre", "nunca", "jamás",
		"quizás", "talvez", "acaso", "posiblemente", "probablemente", "seguramente",
		"sí", "no", "tampoco", "también", "muy", "mucho", "poco", "bastante", "demasiado",
		"más", "menos", "tan", "tanto", "nada", "nadie", "alguien", "algo", "todo", "todos", "todas",
		"hola", "adiós", "gracias", "porfavor", "bueno", "vale", "venga", "oye", "mira",
		"cual", "cuales", "quien", "quienes", "cuyo", "cuya", "cuyos", "cuyas",
		"realmente", "básicamente", "simplemente", "solamente", "únicamente", "precisamente",
		"primero", "segundo", "tercero", "último", "propio", "misma", "mismos", "general",
	},

	"fr": { // French
		// Articles & Demonstratives
		"le", "la", "les", "l'", "un", "une", "des", "du", "de", "d'", "au", "aux",
		"ce", "cet", "cette", "ces", "celui", "celle", "ceux", "celles", "ci", "là",
		// Pronouns
		"je", "tu", "il", "elle", "on", "nous", "vous", "ils", "elles",
		"me", "m'", "te", "t'", "se", "s'", "nous", "vous", "le", "la", "les", "lui", "leur",
		"moi", "toi", "soi", "eux", "y", "en",
		"mon", "ton", "son", "ma", "ta", "sa", "mes", "tes", "ses",
		"notre", "votre", "leur", "nos", "vos", "leurs",
		"lequel", "laquelle", "lesquels", "lesquelles", "auquel", "auxquels",
		// Prepositions & Conjunctions
		"à", "après", "avant", "avec", "chez", "contre", "dans", "de", "depuis", "derrière",
		"devant", "en", "entre", "envers", "hors", "jusque", "malgré", "par", "parmi",
		"pendant", "pour", "sans", "sauf", "selon", "sous", "sur", "vers", "via",
		"mais", "ou", "et", "donc", "or", "ni", "car", "que", "qu'", "quand", "si",
		"comme", "lorsque", "puisque", "quoique", "parce",
		// Verbs (Être/Avoir/Faire/Aller)
		"suis", "es", "est", "sommes", "êtes", "sont", "été", "étais", "était", "étions", "étiez", "étaient",
		"serai", "seras", "sera", "serons", "serez", "seront", "sois", "soit", "soyons", "soyez", "soient",
		"ai", "as", "a", "avons", "avez", "ont", "eu", "avais", "avait", "avions", "aviez", "avaient",
		"aurai", "auras", "aura", "aurons", "aurez", "auront", "aie", "aies", "ait", "ayons", "ayez", "aient",
		"fais", "fait", "faisons", "faites", "font", "fait", "ferai", "feras", "fera",
		"vais", "vas", "va", "allons", "allez", "vont", "allé", "irai", "iras", "ira",
		// Adverbs & Fillers
		"ici", "là", "ailleurs", "partout", "maintenant", "aujourd'hui", "hier", "demain",
		"tôt", "tard", "bientôt", "toujours", "jamais", "souvent", "parfois", "déjà",
		"très", "trop", "peu", "beaucoup", "assez", "plus", "moins", "aussi", "autant",
		"si", "tellement", "tout", "toute", "tous", "toutes", "rien", "personne", "quelqu'un", "quelque",
		"oui", "non", "ne", "pas", "point", "guère", "aucun", "aucune",
		"bonjour", "merci", "pardon", "désolé", "salut", "adieu", "svp", "voilà", "voici",
		"alors", "enfin", "ensuite", "puis", "aussi", "pourtant", "cependant", "néanmoins",
		"vraiment", "simplement", "actuellement", "probablement", "sûrement", "certainement",
		"comment", "pourquoi", "où", "combien", "qui", "quoi", "dont",
	},

	"de": { // German
		// Articles
		"der", "die", "das", "den", "dem", "des", "ein", "eine", "einer", "eines", "einem", "einen",
		// Pronouns
		"ich", "du", "er", "sie", "es", "wir", "ihr", "sie", "Sie",
		"mich", "dich", "ihn", "sie", "es", "uns", "euch", "sie", "Sie",
		"mir", "dir", "ihm", "ihr", "ihm", "uns", "euch", "ihnen", "Ihnen",
		"mein", "dein", "sein", "ihr", "unser", "euer", "ihr", "Ihr",
		"meine", "deine", "seine", "ihre", "unsere", "eure", "ihre", "Ihre",
		"meinen", "deinen", "seinen", "ihren", "unseren", "euren", "ihren", "Ihren",
		"meinem", "deinem", "seinem", "ihrem", "unserem", "eurem", "ihrem", "Ihrem",
		"meines", "deines", "seines", "ihres", "unseres", "eures", "ihres", "Ihres",
		"dieser", "diese", "dieses", "diesen", "diesem", "jenes", "jener", "jede", "jeder", "jedes",
		"man", "jemand", "niemand", "etwas", "nichts", "alles", "welcher", "welche", "welches",
		// Prepositions
		"ab", "an", "auf", "aus", "außer", "bei", "bis", "durch", "entgegen", "entlang",
		"für", "gegen", "gegenüber", "gemäß", "hinter", "in", "mit", "nach", "neben",
		"ohne", "seit", "trotz", "über", "um", "unter", "von", "vor", "während", "wegen", "zu", "zwischen",
		// Conjunctions & Particles (Crucial for German)
		"und", "oder", "aber", "denn", "doch", "sondern", "als", "bevor", "bis", "da", "damit",
		"dass", "ehe", "falls", "indem", "nachdem", "ob", "obwohl", "seit", "seitdem", "sodass",
		"solange", "sooft", "während", "weil", "wenn", "wie", "wo", "wohin", "woher",
		"halt", "eben", "mal", "ja", "nun", "eigentlich", "wohl", "vielleicht", "gar", "bloß",
		// Verbs (Sein/Haben/Werden/Modals)
		"bin", "bist", "ist", "sind", "seid", "war", "warst", "waren", "wart", "gewesen", "wäre",
		"habe", "hast", "hat", "haben", "habt", "hatte", "hattest", "hatten", "hattet", "gehabt", "hätte",
		"werde", "wirst", "wird", "werden", "werdet", "wurde", "wurdest", "wurden", "wurdet", "geworden",
		"kann", "kannst", "können", "konnte", "muss", "musst", "müssen", "musste",
		"soll", "sollst", "sollen", "sollte", "will", "willst", "wollen", "wollte",
		"darf", "darfst", "dürfen", "durfte", "mag", "magst", "mögen", "mochte",
		"mache", "machst", "macht", "machen", "machte", "gemacht", "getan", "gehe", "ging",
		// Adverbs & Fillers
		"hier", "da", "dort", "drüben", "heute", "morgen", "gestern", "jetzt", "nun", "damals",
		"bald", "immer", "nie", "oft", "selten", "manchmal", "gerade", "sofort", "gleich",
		"sehr", "ganz", "zu", "viel", "wenig", "mehr", "weniger", "auch", "noch", "schon", "nur",
		"genau", "richtig", "natürlich", "selbstverständlich", "wirklich", "tatsächlich",
		"hallo", "tschüss", "bitte", "danke", "entschuldigung", "leider", "zum", "zur", "im", "am", "ans",
	},

	"ja": { // Japanese
		// Particles
		"は", "が", "を", "に", "へ", "と", "て", "で", "の", "も", "や", "か", "から", "まで",
		"より", "くらい", "ぐらい", "など", "だけ", "ばかり", "しか", "こそ", "でも", "さえ",
		"なら", "けれど", "けど", "が", "のに", "ので", "から", "ため", "し", "たり", "だり",
		// Pronouns
		"私", "僕", "俺", "あたし", "自分", "あなた", "君", "お前", "彼", "彼女", "彼ら",
		"私たち", "僕ら", "これ", "それ", "あれ", "どれ", "ここ", "そこ", "あそこ", "どこ",
		"こちら", "そちら", "あちら", "どちら", "こっち", "そっち", "あっち", "どっち",
		"いつ", "何", "誰", "どなた",
		// Verb/Adjective Endings & Auxiliaries
		"です", "ます", "でした", "ました", "ません", "でしょう", "だ", "だった", "だろう",
		"ある", "あります", "あった", "ない", "ありません", "なく", "なかった",
		"いる", "います", "いた", "いない", "いません",
		"する", "します", "した", "しない", "しません", "して", "され",
		"なる", "なります", "なった", "ならない", "なりました",
		"行く", "行きます", "行った", "来る", "来ます", "来た",
		"たい", "たかった", "たくない", "ている", "てある", "ておく", "てしまう",
		"れる", "られる", "せる", "させる", "ないで", "なくて",
		// Conjunctions & Connectors
		"そして", "それから", "だから", "しかし", "でも", "けれど", "ところが", "ところで",
		"また", "または", "あるいは", "つまり", "すなわち", "要するに", "例えば",
		"なぜなら", "だって", "なお", "ただし", "もっとも",
		// Common Adverbs/Fillers
		"もう", "まだ", "ずっと", "とても", "すごく", "大変", "かなり", "ずいぶん",
		"まあ", "ちょっと", "少し", "たくさん", "いっぱい", "ほとんど", "大体",
		"いつも", "よく", "しばしば", "たびたび", "時々", "たまに", "全然", "決して",
		"今日", "昨日", "明日", "今", "さっき", "後で", "すぐ", "そろそろ",
		"本当に", "実は", "やはり", "やっぱり", "もちろん", "多分", "きっと", "必ず",
		"はい", "いいえ", "ええ", "うん", "ううん", "そう", "あの", "えっと", "なんか", "なんて",
		"ありがとう", "ごめんなさい", "すみません", "お願いします", "こんにちは", "さようなら",
		"一", "二", "三", "つ", "個", "人", "円", "年", "月", "日",
	},

	"zh": { // Chinese (Simplified + Common Traditional chars)
		// Particles
		"的", "了", "在", "是", "我", "有", "和", "就", "不", "人", "都", "一", "个", "上", "也",
		"得", "之", "着", "著", "吗", "嘛", "呢", "吧", "啊", "啦", "呀", "哇", "么", "们", "地",
		// Pronouns & Demonstratives
		"我", "你", "他", "她", "它", "我们", "你们", "他们", "她们", "它们", "咱们", "大家",
		"这", "那", "哪", "此", "彼", "这里", "那里", "哪里", "这边", "那边", "这儿", "那儿",
		"什么", "谁", "几", "多少", "怎么样", "怎么", "为何",
		"自己", "某", "某人", "每", "各",
		// Prepositions & Conjunctions
		"从", "自", "往", "向", "朝", "在", "于", "至", "到", "离",
		"对于", "关于", "把", "被", "让", "给", "替", "为", "为了", "因", "因为", "由于",
		"按", "按照", "据", "根据", "以", "用", "通过", "经过", "作为",
		"和", "跟", "同", "与", "及", "以及", "或", "或者", "还是",
		"虽然", "但是", "可是", "不过", "然而", "如果", "假如", "要是", "即使", "只要", "只有",
		"无论", "不管", "所以", "因此", "因而", "从而", "然后", "接着", "于是",
		// Adverbs & Common Verbs
		"很", "非常", "特别", "十分", "极", "最", "更", "比较", "太", "真",
		"已经", "曾经", "正在", "将", "将要", "快", "刚", "刚才", "就", "才", "又", "再",
		"也", "还", "倒", "却", "总", "总是", "老是", "往往", "经常", "通常", "平时",
		"没", "没有", "别", "不用", "未必",
		"是", "不是", "有", "没有", "做", "作", "干", "弄", "搞",
		"能", "能够", "会", "可以", "可能", "应该", "应当", "得", "必须", "需要", "愿意", "敢",
		"去", "来", "回", "到", "出", "进", "上", "下", "起", "过",
		// Time & Fillers
		"现在", "目前", "今天", "明天", "昨天", "今年", "明年", "去年", "早上", "晚上", "下午",
		"时候", "时间", "期间", "后来", "以前", "以后", "以来",
		"比如", "例如", "一般", "一样", "这样", "那样",
		"你好", "谢谢", "再见", "对不起", "没关系", "请", "麻烦",
		"些", "种", "位", "张", "条", "只", "次", "遍",
	},

	"hi": { // Hindi
		// Postpositions & Markers
		"کا", "के", "की", "को", "में", "पे", "पर", "से", "ने", "तक", "लिए", "द्वारा", "साथ",
		"का", "के", "की", "वाला", "वाली", "वाले",
		// Pronouns
		"मैं", "तू", "तुम", "आप", "हम", "वह", "वे", "यह", "ये",
		"मेरा", "मेरी", "मेरे", "तुम्हारा", "तुम्हारी", "तुम्हारे", "आपका", "आपकी", "आपके",
		"हमारा", "हमारी", "हमारे", "उसका", "उसकी", "उसके", "इनका", "इनकी", "इनके", "उनका", "उनकी", "उनके",
		"मुझे", "तुझे", "तुम्हें", "हमें", "उसे", "इन्हें", "उन्हें", "किसको", "किसे",
		"स्वयं", "खुद", "अपने", "अपना", "अपनी",
		"कौन", "क्या", "क्यों", "कब", "कहाँ", "कैसे", "कितना", "कितने", "किस", "किन",
		// Verbs (Auxiliaries & Common)
		"है", "हैं", "हूँ", "हो", "था", "थी", "थे", "थीं",
		"गा", "गी", "गे", "चाहिए", "सकता", "सकते", "सकती", "सका", "सके",
		"कर", "करो", "करता", "करते", "करती", "किया", "किये", "की", "करूँगा", "करेंगे",
		"हो", "हुआ", "हुए", "हुई", "होता", "होते", "होती", "होगा", "होंगे", "होना",
		"जा", "जाता", "जाते", "जाती", "गया", "गए", "गई", "जाऊँगा", "जाएंगे",
		"दे", "देता", "दिया", "ले", "लेता", "लिया",
		// Adverbs & Conjunctions
		"और", "तथा", "एवं", "या", "अथवा", "कि", "लेकिन", "मगर", "परन्तु", "किन्तु", "बल्कि",
		"अगर", "यदि", "तो", "वरना", "अन्यथा", "क्योंकि", "चूँकि", "इसलिए", "ताकि",
		"भी", "ही", "तो", "भर", "मात्र", "केवल", "सिर्फ",
		"हाँ", "नहीं", "ना", "मत", "जी",
		"आज", "कल", "परसों", "अभी", "अब", "जब", "तब", "कभी", "सदा", "हमेशा",
		"यहाँ", "वहाँ", "जहाँ", "कहाँ", "इधर", "उधर", "जिधर", "तिधर",
		"बहुत", "कम", "ज्यादा", "अधिक", "थोड़ा", "काफी", "बिलकुल", "लगभग",
		"शायद", "संभवतः", "जरूर", "अवश्य", "सचमुच", "वास्तव",
		"नमस्ते", "धन्यवाद", "शुक्रिया", "कृपया",
	},

	"bn": { // Bengali
		// Pronouns
		"আমি", "তুমি", "তুই", "আপনি", "সে", "তিনি", "ও", "ইনি", "এ", "আমরা", "তোমরা", "আপনারা", "তারা", "তাঁরা", "ওরা", "এরা",
		"আমার", "তোমার", "তোর", "আপনার", "তার", "তাঁহার", "ওর", "এর", "আমাদের", "তোমাদের", "আপনাদের", "তাদের", "তাঁদের", "ওদের", "এদের",
		"আমাকে", "তোমাকে", "তোকে", "আপনাকে", "তাকে", "তাঁকে", "ওকে", "একে",
		"নিজে", "নিজের", "স্বয়ং", "কার", "কাকে", "কে", "কি", "কী", "কেন", "কখন", "কোথায়", "কিভাবে", "কোন",
		// Case Markers/Postpositions (Often separate words in text)
		"তে", "এ", "য়", "র", "এর", "কে", "রে",
		"জন্য", "দিয়ে", "দিয়া", "কর্তৃক", "থেকে", "হতে", "চেয়ে", "দ্বারা", "সঙ্গে", "সাথে", "কাছে",
		"মধ্যে", "ভিতরে", "বাইরে", "উপরে", "নিচে", "সামনে", "পিছনে", "পরে", "আগে", "বিনা", "ছাড়া", "ব্যতীত",
		"মত", "মতন", "নিয়ে", "বাদে", "প্রতি",
		// Verbs (Auxiliaries & Common)
		"হয়", "হন", "হও", "হস", "হল", "হলো", "হয়ে", "হওয়া", "হবে", "হয়েছিল", "থাক", "থাকা", "আছে", "আছেন", "ছিল", "ছিলেন",
		"কর", "করা", "করে", "করেন", "করো", "করছ", "করছে", "করছেন", "করল", "করলেন", "করব", "করবেন",
		"বল", "বলা", "বলে", "বলেন", "বলল", "বললেন", "যা", "যাওয়া", "যায়", "যান", "গেল", "গেলেন", "যাবে", "যাবেন",
		"পার", "পারা", "পারে", "পারেন", "পারব", "পারবেন", "উচিত", "দরকার", "প্রয়োজন",
		// Conjunctions & Adverbs
		"এবং", "ও", "আর", "বা", "অথবা", "কিংবা", "নাকি", "কিন্তু", "তবে", "তবু", "তবুও",
		"যে", "যদি", "যদিও", "কারণ", "তাই", "সুতরাং", "ফলে", "যেন", "বরং",
		"না", "নি", "নাই", "নয়", "নন",
		"আজ", "কাল", "এখন", "তখন", "কখন", "যখন", "সবসময়", "কখনও", "কখনো",
		"এখানে", "ওখানে", "সেখানে", "কোথায়",
		"খুব", "অনেক", "বেশি", "কম", "একটু", "প্রায়", "মাত্র", "শুধু", "কেবল", "হয়তো", "সম্ভবত", "অবশ্যই",
		"টি", "টা", "খানা", "খানি", "গুলো", "গুলি", "জন",
		"নমস্কার", "ধন্যবাদ", "স্বাগতম", "হ্যালো",
	},

	"ta": { // Tamil
		// Pronouns
		"நான்", "நீ", "நீவிர்", "நீங்கள்", "அவன்", "அவள்", "அவர்", "அது", "நாங்கள்", "நாம்", "அவர்கள்", "அவை", "இவன்", "இவள்", "இவர்", "இது", "இவர்கள்", "இவை",
		"என்", "உன்", "உங்கள்", "அவன்", "அவள்", "அவர்", "அதன்", "எங்கள்", "நம்", "அவர்கள்", "அவற்றின்",
		"என்னை", "உன்னை", "உங்களை", "அவனை", "அவளை", "அவரை", "அதை", "எங்களை", "நம்மை", "அவர்களை", "அவற்றை",
		"எனக்கு", "உனக்கு", "உங்களுக்கு", "அவனுக்கு", "அவளுக்கு", "அவருக்கு", "அதற்கு", "எங்களுக்கு", "நமக்கு", "அவர்களுக்கு", "அவற்றுக்கு",
		"யார்", "எவர்", "என்ன", "ஏன்", "எப்படி", "எங்கு", "எப்போது", "எது", "எவை",
		// Postpositions/Particles (Often agglutinated, but frequently separate in web text)
		"உடன்", "கூட", "கொண்டு", "மூலம்", "ஆல்", "இலிருந்து", "இருந்து", "விடை", "காட்டிலும்",
		"பற்றி", "குறித்து", "க்காக", "ஆக", "ஓடு", "இல்", "இடம்", "கண்", "முன்", "பின்", "மேல்", "கீழ்",
		"உள்ள", "அற்று", "இன்றி", "தவிர", "எல்லாம்", "அனைத்தும்", "முழுவதும்",
		// Conjunctions
		"மற்றும்", "மேலும்", "ஆனால்", "எனவே", "ஆகவே", "அதனால்", "ஏனெனில்", "என்றால்", "எனில்",
		"அல்லது", "இல்லையெனில்", "ஆனாலும்", "எனினும்", "இருப்பினும்", "போல", "போல்", "மாறாக", "தவிர",
		"உம்", "ஓ", "ஆவது",
		// Verbs (Auxiliaries & Common)
		"இரு", "இருக்க", "இருக்கிறது", "இருக்கிறார்கள்", "இருந்தது", "இருந்தன", "இருப்பார்", "இருப்பார்கள்",
		"செய்", "செய்ய", "செய்கிறது", "செய்தது", "செய்யும்", "செய்கிறார்கள்",
		"வா", "வர", "வருகிறது", "வந்தது", "வரும்", "வருகிறார்கள்",
		"போ", "போக", "போகிறது", "போனது", "போகும்", "போகிறார்கள்",
		"படு", "பட", "பட்டது", "படுகிறது", "வேண்டும்", "வேண்டாம்", "முடியும்", "முடியாது",
		"உள்ளது", "உள்ளன", "இல்லை", "அல்ல",
		// Adverbs & Time
		"இன்று", "நாளை", "நேற்று", "இப்போது", "தற்போது", "அப்போது", "எப்போது",
		"இங்கு", "அங்கு", "எங்கு", "இங்கே", "அங்கே", "எங்கே",
		"மிக", "மிகவும்", "ரொம்ப", "அதிக", "குறைந்த", "சில", "பல", "கொஞ்சம்",
		"ஆன", "ஆகிய", "என்ற", "எனப்படும்",
		"வணக்கம்", "நன்றி", "தயவுசெய்து", "ஆம்",
	},

	"ru": { // Russian
		// Pronouns (Cases are crucial here)
		"я", "меня", "мне", "мной", "мною",
		"ты", "тебя", "тебе", "тобой", "тобою",
		"он", "его", "него", "ему", "нему", "им", "ним", "нем",
		"она", "её", "нее", "ей", "ней", "ею", "нею",
		"оно", "него", "нему", "ним", "нем",
		"мы", "нас", "нам", "нами",
		"вы", "вас", "вам", "вами",
		"они", "их", "них", "им", "ними",
		"себя", "себе", "собой", "собою",
		"мой", "моя", "мое", "мои", "моего", "моей", "моих", "моему", "моим",
		"твой", "твоя", "твое", "твои",
		"наш", "наша", "наше", "наши",
		"ваш", "ваша", "ваше", "ваши",
		"свой", "своя", "свое", "свои",
		"кто", "кого", "кому", "кем", "ком",
		"что", "чего", "чему", "чем", "чём",
		"этот", "эта", "это", "эти", "этого", "этой", "этих", "этому", "этим", "этом",
		"тот", "та", "то", "те", "того", "той", "тех", "тому", "тем", "том",
		"такой", "такая", "такое", "такие",
		"весь", "вся", "всё", "все", "всего", "всей", "всем", "всеми", "всём",
		"каждый", "любой", "сам", "сама", "само", "сами",
		// Prepositions
		"в", "во", "на", "с", "со", "к", "ко", "у", "о", "об", "обо", "за", "по", "из", "изо",
		"от", "ото", "до", "для", "без", "безо", "над", "надо", "под", "подо", "при", "про",
		"через", "между", "перед", "передо", "вокруг", "после", "кроме", "вместо", "сквозь",
		// Conjunctions & Particles
		"и", "а", "но", "да", "или", "либо", "ни", "как", "так", "что", "чтобы", "если", "когда",
		"где", "куда", "откуда", "пока", "лишь", "только", "хоть", "хотя", "будто", "словно",
		"же", "ли", "бы", "то", "вот", "ведь", "даже", "ну", "вон", "уж",
		"не", "нет", "ни", "вовсе",
		// Verbs (Be/Have/Common)
		"быть", "был", "была", "было", "были", "буду", "будешь", "будет", "будем", "будете", "будут", "будь", "будьте",
		"есть", "суть", "являться", "является", "являются",
		"мочь", "могу", "можешь", "может", "можем", "можете", "могут", "мог", "могла", "могло", "могли",
		"хотеть", "хочу", "хочешь", "хочет", "хотим", "хотите", "хотят",
		"стать", "стал", "стала", "стало", "стали",
		// Adverbs & Fillers
		"здесь", "тут", "там", "теперь", "сейчас", "тогда", "потом", "уже", "еще", "вчера", "сегодня", "завтра",
		"очень", "много", "мало", "больше", "меньше", "слишком", "почти", "совсем", "вообще",
		"просто", "конечно", "наверное", "возможно", "кстати", "значит",
		"спасибо", "пожалуйста", "здравствуйте", "привет", "пока",
		"один", "два", "три", "раз",
	},

	"ar": { // Arabic
		// Pronouns (Attached & Detached)
		"أنا", "أنت", "أنتَ", "أنتِ", "هو", "هي", "نحن", "أنتم", "أنتما", "أنتن", "هم", "هما", "هن",
		"إياي", "إياك", "إياه", "إياها", "إيانا", "إياكم", "إياهم",
		"هذا", "هذه", "هذان", "هاتان", "هؤلاء", "ذلك", "تلك", "أولئك",
		"الذي", "التي", "الذين", "اللاتي", "اللائي", "اللواتي", "من", "ما",
		// Prepositions
		"في", "من", "إلى", "على", "عن", "ب", "بـ", "ل", "لـ", "ك", "كـ", "مع", "دون", "بدون", "عند", "لدى",
		"بين", "حول", "خلال", "منذ", "حتى", "نحو", "ضد", "عبر",
		"أمام", "خلف", "وراء", "فوق", "تحت", "يمين", "يسار", "جانب", "وسط", "داخل", "خارج", "بعد", "قبل",
		// Conjunctions & Particles
		"و", "أو", "أم", "لكن", "ولكن", "بل", "ثم", "ف", "فـ", "إذن",
		"أن", "إن", "لأن", "كي", "لكي", "حيث", "لو", "لولا", "إذا", "كلما", "لما",
		"لا", "ما", "لم", "لن", "ليس", "ليست", "ليسوا", "غير", "سوى", "إلا",
		"هل", "كيف", "أين", "متى", "كم", "لماذا", "أي", "ماذا",
		// Verbs (To be/Auxiliary implied or explicit)
		"كان", "كانت", "كانوا", "كنت", "يكون", "تكون", "أكون", "نكون", "يكونون", "سيكون", "قد", "سوف",
		"أصبح", "صار", "ليس", "مازال", "يجب", "ينبغي", "ممكن", "يستطيع",
		"قال", "يقول", "قلت", "فعل", "يفعل", "تم", "يتم",
		// Adverbs & Common Words
		"الآن", "اليوم", "أمس", "غدا", "دائما", "أبدا", "حينا", "سابقا", "لاحقا", "حاليا",
		"هنا", "هناك", "هنالك",
		"جدا", "كثيرا", "قليلا", "تقريبا", "فقط", "أيضا", "كذلك",
		"نعم", "لا", "بلى", "كلا", "أجل",
		"شكرا", "عفوا", "مرحبا", "أهلا", "السلام", "عليكم", "صباح", "مساء", "الخير",
		"كل", "بعض", "نفس", "عين", "جميع", "عامة", "خاصة",
		"واحد", "اثنان", "أول", "ثاني", "آخر", "أخرى",
	},

	"pt": { // Portuguese
		// Articles
		"o", "a", "os", "as", "um", "uma", "uns", "umas",
		"ao", "à", "aos", "às", "do", "da", "dos", "das", "no", "na", "nos", "nas", "pelo", "pela", "pelos", "pelas",
		"dum", "duma", "duns", "dumas", "num", "numa", "nuns", "numas",
		// Pronouns
		"eu", "tu", "ele", "ela", "nós", "vós", "eles", "elas", "você", "vocês",
		"me", "te", "se", "lhe", "nos", "vos", "lhes", "o", "a", "os", "as",
		"mim", "ti", "si", "comigo", "contigo", "consigo", "conosco", "convosco",
		"meu", "minha", "meus", "minhas", "teu", "tua", "teus", "tuas", "seu", "sua", "seus", "suas",
		"nosso", "nossa", "nossos", "nossas", "vosso", "vossa", "vossos", "vossas",
		"este", "esta", "estes", "estas", "isto", "esse", "essa", "esses", "essas", "isso",
		"aquele", "aquela", "aqueles", "aquelas", "aquilo",
		"qual", "quais", "que", "quem", "cujo", "cuja", "cujos", "cujas", "onde",
		"algo", "alguém", "algum", "alguma", "alguns", "algumas", "nada", "ninguém", "nenhum", "nenhuma",
		"tudo", "todo", "toda", "todos", "todas", "outro", "outra", "outros", "outras", "mesmo", "mesma",
		// Prepositions & Conjunctions
		"a", "ante", "após", "até", "com", "contra", "de", "desde", "em", "entre", "para", "perante",
		"por", "sem", "sob", "sobre", "trás",
		"e", "nem", "mas", "porém", "todavia", "contudo", "entretanto", "ou", "ora", "logo", "portanto",
		"porque", "pois", "porquanto", "que", "se", "caso", "embora", "conquanto", "conforme", "segundo",
		"como", "quando", "enquanto", "depois", "onde",
		// Verbs (Ser/Estar/Ter/Haver/Ir)
		"sou", "és", "é", "somos", "sois", "são", "fui", "foi", "fomos", "foram", "era", "eras", "éramos", "eram", "ser", "sido",
		"estou", "estás", "está", "estamos", "estais", "estão", "estive", "esteve", "estivemos", "estiveram", "estava",
		"tenho", "tens", "tem", "temos", "tendes", "têm", "tive", "teve", "tivemos", "tiveram", "tinha", "ter",
		"hei", "hás", "há", "havemos", "haveis", "hão", "houve", "havia",
		"vou", "vais", "vai", "vamos", "ides", "vão", "fui", "foi", "ir", "ia",
		"posso", "podes", "pode", "podemos", "podem", "fazer", "faço", "faz", "dizer", "disse",
		// Adverbs & Fillers
		"aqui", "cá", "aí", "ali", "lá", "agora", "já", "ainda", "hoje", "ontem", "amanhã",
		"cedo", "tarde", "sempre", "nunca", "jamais", "talvez", "acaso", "quiçá",
		"sim", "não", "muito", "pouco", "bastante", "demais", "mais", "menos", "tão", "tanto", "quão",
		"bem", "mal", "assim", "depressa", "devagar", "então", "pois",
		"olá", "oi", "obrigado", "obrigada", "porfavor", "desculpe", "comlicença", "tchau", "adeus",
	},

	"pa": { // Punjabi (Gurmukhi)
		// Pronouns
		"ਮੈਂ", "ਤੂੰ", "ਤੁਸੀਂ", "ਅਸੀਂ", "ਉਹ", "ਇਹ", "ਆਪ", "ਸਾਨੂੰ", "ਤੁਹਾਨੂੰ", "ਮੈਨੂੰ", "ਤੈਨੂੰ", "ਉਸਨੂੰ", "ਇਸਨੂੰ", "ਉਨ੍ਹਾਂ", "ਇਨ੍ਹਾਂ",
		"ਮੇਰਾ", "ਮੇਰੀ", "ਮੇਰੇ", "ਮੇਰੀਆਂ", "ਤੇਰਾ", "ਤੇਰੀ", "ਤੇਰੇ", "ਤੇਰੀਆਂ", "ਤੁਹਾਡਾ", "ਤੁਹਾਡੀ", "ਤੁਹਾਡੇ", "ਤੁਹਾਡੀਆਂ",
		"ਸਾਡਾ", "ਸਾਡੀ", "ਸਾਡੇ", "ਸਾਡੀਆਂ", "ਉਸਦਾ", "ਉਸਦੀ", "ਉਸਦੇ", "ਉਨ੍ਹਾਂਦਾ", "ਕਿਸਦਾ", "ਕਿਸਦੀ",
		"ਕੌਣ", "ਕੀ", "ਕਿਉਂ", "ਕਦੋਂ", "ਕਿੱਥੇ", "ਕਿਵੇਂ", "ਕਿਹੜਾ", "ਕਿਹੜੀ", "ਕਿਹੜੇ", "ਕਿੰਨਾ", "ਕਿੰਨੇ",
		// Postpositions
		"ਦਾ", "ਦੇ", "ਦੀ", "ਦੀਆਂ", "ਨੂੰ", "ਨੇ", "ਤੋਂ", "ਕੋਲੋਂ", "ਵਿੱਚ", "ਵਿਚ", "ਤੇ", "ਉੱਤੇ", "ਹੇਠਾਂ", "ਥੱਲੇ",
		"ਨਾਲ", "ਲਈ", "ਬਾਰੇ", "ਵਰਗਾ", "ਵਾਂਗ", "ਬਿਨਾਂ", "ਸਮੇਤ", "ਦੁਆਰਾ", "ਰਾਹੀਂ", "ਪਾਸ",
		"ਵਾਲਾ", "ਵਾਲੀ", "ਵਾਲੇ", "ਵਾਲੀਆਂ",
		// Verbs (Auxiliaries & Common)
		"ਹੈ", "ਹਨ", "ਹਾਂ", "ਹੋ", "ਸੀ", "ਸਨ", "ਸਾਂ", "ਸੋ",
		"ਹੋਵੇ", "ਹੋਵੇਗਾ", "ਹੋਵੇਗੀ", "ਹੋਵੋਗੇ", "ਹੋਣਗੇ", "ਸਕਦਾ", "ਸਕਦੇ", "ਸਕਦੀ",
		"ਕਰ", "ਕਰਦਾ", "ਕਰਦੀ", "ਕਰਦੇ", "ਕਰਿਆ", "ਕੀਤਾ", "ਕਰਨ", "ਕਰਨਾ", "ਕਰਨੀ", "ਕਰਾਂਗਾ", "ਕਰਾਂਗੇ",
		"ਜਾ", "ਜਾਂਦਾ", "ਜਾਂਦੀ", "ਜਾਂਦੇ", "ਗਿਆ", "ਗਏ", "ਗਈ", "ਜਾਵੇਗਾ", "ਜਾਣਾ",
		"ਆ", "ਆਉਂਦਾ", "ਆਇਆ", "ਲੈ", "ਦੇ", "ਦਿੱਤਾ", "ਲਿਆ",
		// Conjunctions & Adverbs
		"ਅਤੇ", "ਤੇ", "ਪਰ", "ਜਾਂ", "ਕਿ", "ਜੇ", "ਜੇਕਰ", "ਤਾਂ", "ਕਿਉਂਕਿ", "ਇਸਲਈ", "ਨਾਲੇ", "ਸਗੋਂ", "ਭਾਵੇਂ", "ਫਿਰ",
		"ਅੱਜ", "ਕੱਲ੍ਹ", "ਹੁਣ", "ਕਦੇ", "ਜਦੋਂ", "ਤਦੋਂ", "ਸਦਾ", "ਹਮੇਸ਼ਾ",
		"ਇੱਥੇ", "ਉੱਥੇ", "ਕਿੱਥੇ", "ਜਿੱਥੇ", "ਇਧਰ", "ਉਧਰ",
		"ਬਹੁਤ", "ਘੱਟ", "ਵੱਧ", "ਥੋੜ੍ਹਾ", "ਬਸ", "ਸਿਰਫ", "ਕੇਵਲ", "ਬਿਲਕੁਲ", "ਸ਼ਾਇਦ", "ਜ਼ਰੂਰ",
		"ਹਾਂ", "ਨਹੀਂ", "ਜੀ", "ਅੱਛਾ", "ਠੀਕ", "ਸ਼ੁਕਰੀਆ", "ਧੰਨਵਾਦ", "ਸਤਿ ਸ੍ਰੀ ਅਕਾਲ", "ਕਿਰਪਾ", "ਮਿਹਰਬਾਨੀ",
	},

	"pa_ps": { // Punjabi (Shahmukhi)
		// Pronouns
		"میں", "توں", "تسیں", "اسیں", "اوہ", "اے", "آپ", "سانوں", "تہانوں", "مینوں", "تینوں", "اونوں", "اینوں", "اونہاں", "اینہاں",
		"میرا", "میری", "میرے", "میریاں", "تیرا", "تیری", "تیرے", "تیریاں", "تہاڈا", "تہاڈی", "تہاڈے", "تہاڈیاں",
		"ساڈا", "ساڈی", "ساڈے", "ساڈیاں", "اودا", "اودی", "اودے", "کسدا", "کسدی",
		"کون", "کی", "کیوں", "کدوں", "کتھے", "کویں", "کیہڑا", "کیہڑی", "کیہڑے", "کنا", "کنے",
		// Postpositions
		"دا", "دے", "دی", "دیاں", "نوں", "نے", "توں", "کولوں", "وچ", "تے", "اتے", "تھلے",
		"نال", "لئی", "بارے", "ورگا", "وانگ", "بنا", "سمیت", "دوارا", "راہیں", "پاس",
		"والا", "والی", "والے", "والیاں",
		// Verbs
		"ہے", "نیں", "ہاں", "ہو", "سی", "سن", "ساں", "سو",
		"ہووے", "ہووےگا", "ہووےگی", "ہوووگے", "ہونگے", "سکدا", "سکدے", "سکدی",
		"کر", "کردا", "کردی", "کردے", "کریا", "کیتا", "کرن", "کرنا", "کرنی", "کراں گا", "کراں گے",
		"جا", "جاندا", "جاندی", "جاندے", "گیا", "گئے", "گئی", "جاوےگا", "جانا",
		"آ", "آوندا", "آیا", "لے", "دے", "دتا", "لیا",
		// Conjunctions & Adverbs
		"تے", "پر", "یا", "کہ", "جے", "جیکر", "تاں", "کیونہ", "ایس لئی", "نالے", "سگوں", "بھاویں", "فیر",
		"اج", "کل", "ہن", "کدی", "جدوں", "تدوں", "سدا", "ہمیش",
		"ایتھے", "اوتھے", "کتھے", "جتھے", "ایدھر", "اودھر",
		"بہت", "گھٹ", "ودھ", "تھوڑا", "بس", "صرف", "کیول", "بالکل", "شاید", "ضرور",
		"ہاں", "نہیں", "جی", "اچھا", "ٹھیک", "شکریہ", "مہربانی",
	},

	"mr": { // Marathi
		// Pronouns
		"मी", "तू", "तुम्ही", "आपण", "आम्ही", "तो", "ती", "ते", "त्या", "हा", "ही", "हे", "ह्या", "जो", "जी", "जे", "ज्या",
		"मला", "तुला", "तुम्हाला", "आम्हाला", "त्याला", "तिला", "त्यांना", "ह्याला", "हिला", "ह्यांना",
		"माझा", "माझी", "माझे", "माझ्या", "तुझा", "तुझी", "तुझे", "तुझ्या", "तुमचा", "तुमची", "तुमचे", "तुमच्या", "आमचा", "आमची", "आमचे", "आमच्या",
		"त्याचा", "त्याची", "त्याचे", "त्याच्या", "तिचा", "तिची", "तिचे", "तिच्या", "कोणाचा", "कोणाची",
		"कोण", "काय", "कसा", "कशी", "कसे", "कुठे", "केव्हा", "कधी", "का", "किती",
		"स्वतः", "स्वतःचा",
		// Postpositions (Suffixes often detached in tokenization or listed as standalone connectors)
		"चा", "ची", "चे", "च्या", "ला", "ने", "नी", "शी", "हून", "तून",
		"मध्ये", "वर", "खाली", "आत", "बाहेर", "कडे", "जवळ", "पाशी", "पेक्षा", "सारखा", "साठी", "करिता", "विना", "शिवाय",
		"मुळे", "द्वारे", "कडून", "बद्दल", "विषयी", "विरुद्ध", "समोर", "मागे", "पुढे", "नंतर", "आधी", "पर्यंत",
		// Verbs (Auxiliaries - To be/To do/To become)
		"आहे", "आहेत", "आहोत", "आहास", "नाही", "नाहीत", "नाहीस", "होता", "होती", "होते", "होत्या", "नव्हता", "नव्हती", "नव्हते",
		"असेल", "असतील", "असावे", "असावा", "असावी", "नसेल", "नसावे",
		"कर", "करा", "करतो", "करते", "करतात", "करत", "केले", "केला", "केली", "करणार", "कराल", "करावे",
		"हो", "होणे", "झाले", "झाला", "झाली", "होईल", "होणार",
		"ये", "येतो", "येते", "येतात", "आला", "आली", "आले",
		"जा", "जातो", "जाते", "जातात", "ग्रेला", "गेली", "गेले", "जाईल", "जाणार",
		"शकत", "शकतो", "शकते", "शकतात", "पाहिजे",
		// Conjunctions & Adverbs
		"आणि", "व", "किंवा", "पण", "परंतु", "मात्र", "म्हणून", "की", "जर", "तर", "जरी", "तरी", "कारण", "का",
		"आज", "उद्या", "काल", "आता", "लवकर", "उशिरा", "नेहमी", "कधीही", "पुन्हा",
		"इथे", "तिथे", "कुठे", "जेथे", "तेथे", "इकडे", "तिकडे",
		"खूप", "जास्त", "कमी", "थोडे", "फार", "अगदी", "फक्त", "केवळ", "मुळीच", "शक्यतो", "बहुदा", "कदाचित", "नक्की", "खरंच",
		"नमस्कार", "धन्यवाद", "कृपया", "हो", "नाही", "अरे", "अहो",
	},

	"gu": { // Gujarati
		// Pronouns
		"હું", "તમે", "તું", "અમે", "આપણે", "તે", "તેઓ", "આ", "પેલું",
		"મને", "તમને", "તને", "અમને", "આપણને", "તેને", "તેઓને",
		"મારું", "મારા", "મારી", "તમારું", "તમારા", "તમારી", "તારું", "તારા", "તારી", "અમારું", "અમારા", "અમારી", "આપણું",
		"તેનું", "તેના", "તેની", "તેઓનું", "તેઓના", "તેઓની", "કોનું", "કોના",
		"કોણ", "શું", "ક્યાં", "ક્યારે", "કેમ", "કેવું", "કેવી", "કેવો", "કેટલું", "કેટલા",
		"પોતે", "પોતાનું",
		// Postpositions & Case Markers
		"નો", "ની", "નું", "ના", "ને", "થી", "માં", "પર", "ઉપર", "નીચે",
		"માટે", "વડે", "દ્વારા", "થકી", "સાથે", "વિના", "વગર", "સિવાય",
		"પાસે", "નજીક", "સામે", "પાછળ", "આગળ", "વચ્ચે", "તરફ", "સુધી", "બાદ", "પછી", "પહેલાં",
		// Verbs (Auxiliaries & Common)
		"છે", "છું", "છીએ", "છો", "નથી",
		"હતો", "હતી", "હતું", "હતા", "હશે", "હોય",
		"કર", "કરો", "કરે", "કરવું", "કર્યો", "કરી", "કર્યું", "કરશે",
		"થવું", "થાય", "થયો", "થઈ", "થયું", "થશે",
		"જવું", "જાય", "ગયો", "ગઈ", "ગયું", "જશે",
		"આવવું", "આવે", "આવ્યો", "આવી", "આવ્યું", "આવશે",
		"શકે", "શકવું", "જોઈએ",
		// Conjunctions & Adverbs
		"અને", "ને", "કે", "પણ", "પરંતુ", "છતાં", "તો", "જો", "તો", "કારણ", "કેમકે", "એટલે", "તેથી", "માટે", "અથવા", "યા",
		"આજે", "કાલે", "ગઈકાલે", "આવતીકાલે", "હવે", "ત્યારે", "જ્યારે", "ક્યારેક", "હંમેશા",
		"અહીં", "ત્યાં", "ક્યાંક", "જ્યાં", "ત્યાં",
		"બહુ", "ખૂબ", "વધારે", "ઓછું", "થોડું", "જરા", "સાવ", "બિલકુલ", "ફક્ત", "માત્ર", "કદાચ", "ચોક્કસ", "ખરેખર",
		"હા", "ના", "જી", "નમસ્તે", "આભાર", "સ્વાગત", "મહેરબાની",
	},
}

// NewNormalizer initializes the filter list
func NewNormalizer(language Language) *Normalizer {
	// Your ultra-expanded list (abbreviated for the example)

	n := &Normalizer{
		fluffMap: make(map[string]struct{}),
	}

	// Populate the fluffMap for O(1) lookups
	if words, exists := Fluff[language]; exists {
		for _, w := range words {
			n.fluffMap[w] = struct{}{}
		}
	}

	return n
}

// Normalize cleans a string and returns only the "meaningful" tokens
func (n *Normalizer) Normalize(input string,
) string {
	// 1. Lowercase
	input = strings.ToLower(input)

	// 2. Remove possessives (user's -> user)
	input = strings.ReplaceAll(input, "'s", "")
	input = strings.ReplaceAll(input, "'ve", "")
	input = strings.ReplaceAll(input, "'re", "")
	input = strings.ReplaceAll(input, "'ll", "")
	input = strings.ReplaceAll(input, "n't", "")
	input = strings.ReplaceAll(input, "'d", "")
	input = strings.ReplaceAll(input, "'m", "")
	input = strings.ReplaceAll(input, "'em", "")

	// 3. Remove punctuation using regex
	reg := regexp.MustCompile(`[^a-z0-9\s]+`)
	input = reg.ReplaceAllString(input, "")

	// 4. Tokenize
	words := strings.Fields(input)

	// 5. Filter fluff
	var result []string
	for _, w := range words {
		if _, isFluff := n.fluffMap[w]; !isFluff {
			result = append(result, w)
		}
	}

	return strings.Join(result, " ")
}
