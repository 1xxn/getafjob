package planner

import "regexp"

// rule maps field labels (normalized: lowercase, umlauts folded) to a profile key. The first matching rule wins,
// so specific labels come before general ones.
type rule struct {
	key  string
	rx   *regexp.Regexp
	kind string // "" any field, "bool" a yes/no fact, "file" a document, "text" a text block
}

func r(key, kind, pattern string) rule {
	return rule{key: key, kind: kind, rx: regexp.MustCompile(pattern)}
}

var rules = []rule{
	// documents
	r("cover_letter", "file", `cover letter|anschreiben|motivationsschreiben`),
	r("certificates", "file", `certificat|zeugnis|zertifikat|transcript`),
	r("cv", "file", `resume|\bcv\b|lebenslauf|curriculum`),
	// yes/no facts; "do you need a work permit" is about sponsorship, "are you permitted to work" about authorization
	r("work.needs_sponsorship", "bool", `sponsor|\bvisa\b|\bvisum\b|(need|require|benoetig|brauch)\w* .*(permit|authori[sz]ation|erlaubnis|genehmigung)`),
	r("work.work_authorized_eu", "bool", `(authori[sz]ed|eligible|permitted|legally able|berechtigt) .*(to )?work|work (permit|authori[sz]ation)|arbeitserlaubnis|arbeitsgenehmigung`),
	r("work.willing_to_relocate", "bool", `relocat|umzug|umzuziehen|umziehen`),
	r("preferences.talent_pool", "bool", `talent ?pool|future (openings|opportunities|positions)|zukuenftige (stellen|positionen|vakanzen)`),
	r("preferences.diversity", "diversity", `gender|geschlecht|ethnic|\brace\b|veteran|disabilit|behinderung|hispanic|latin|sexual orientation`),
	// text blocks
	r("cover_letter", "text", `cover letter|anschreiben|motivation|why do you want|why are you interested|warum (moechtest|moechten|willst)`),
	// profile facts
	r("personal.first_name", "", `\b(first|given) ?name\b|\bvorname\b`),
	r("personal.last_name", "", `\b(last|family|sur) ?name\b|\bnachname\b|\bfamilienname\b`),
	r("personal.email", "", `e ?mail`),
	r("personal.phone", "", `phone|telefon|\bmobil|handy`),
	r("personal.linkedin", "", `linkedin`),
	r("personal.github", "", `github`),
	r("personal.website", "", `website|portfolio|homepage|webseite|personal site`),
	r("personal.pronouns", "", `pronoun|pronomen`),
	r("personal.postal_code", "", `\bzip\b|postal|postleitzahl|\bplz\b`),
	r("personal.street", "", `street|strasse|address line|anschrift|\badresse\b|^address$`),
	r("personal.city", "", `\bcity\b|\bort\b|\bstadt\b|wohnort|\btown\b|^location\b`),
	r("personal.country", "", `country|\bland\b|wohnsitzland`),
	r("personal.full_name", "", `^(full )?name\b|^your name|vollstaendiger name|^name$`),
	r("work.current_title", "", `current (job )?(title|role|position)|aktuelle (position|stelle|berufsbezeichnung)|jobtitel`),
	r("work.current_employer", "", `current (employer|company)|aktuelle[rn]? (arbeitgeber|firma|unternehmen)`),
	r("work.years_total", "", `years of (professional )?experience|berufserfahrung|jahre (an )?erfahrung`),
	r("work.notice_period", "", `notice period|kuendigungsfrist`),
	r("work.earliest_start", "", `start date|available (to start|from)|availability|verfuegbar ab|eintrittstermin|startdatum|fruehest`),
	r("work.salary_expectation", "", `salary|gehalt|compensation|verguetung`),
	r("work.drivers_license", "", `driv\w* licen|fuehrerschein`),
}
