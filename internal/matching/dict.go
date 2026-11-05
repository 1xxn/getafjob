package matching

// roleGroups are job titles that mean the same role, in English and German. A wanted title that names one of
// them makes the others count as synonyms.
var roleGroups = [][]string{
	{"software engineer", "software developer", "softwareentwickler", "software entwickler", "softwareingenieur",
		"anwendungsentwickler", "programmer", "programmierer", "application developer"},
	{"backend engineer", "backend developer", "backend entwickler", "backendentwickler", "server engineer"},
	{"frontend engineer", "frontend developer", "frontend entwickler", "frontendentwickler", "web developer",
		"webentwickler", "ui engineer"},
	{"fullstack", "full stack", "fullstackentwickler"},
	{"devops", "site reliability", "sre", "platform engineer", "infrastructure engineer", "cloud engineer",
		"systems engineer", "plattform engineer"},
	{"data engineer", "dateningenieur", "analytics engineer", "etl developer"},
	{"data scientist", "machine learning engineer", "ml engineer", "ai engineer", "data science"},
	{"data analyst", "datenanalyst", "business intelligence", "bi analyst", "bi developer"},
	{"system administrator", "systemadministrator", "sysadmin", "it administrator", "linux administrator",
		"administrator", "systembetreuer"},
	{"it support", "helpdesk", "service desk", "first level support", "second level support", "it supporter",
		"desktop support", "it techniker"},
	{"security engineer", "it security", "cyber security", "cybersecurity", "informationssicherheit",
		"security analyst", "penetration tester"},
	{"network engineer", "netzwerkadministrator", "netzwerktechniker", "network administrator"},
	{"qa engineer", "test engineer", "softwaretester", "quality assurance", "testautomatisierer", "test automation"},
	{"product manager", "product owner", "produktmanager"},
	{"project manager", "projektmanager", "projektleiter", "scrum master", "delivery manager"},
	{"mobile developer", "ios developer", "android developer", "app entwickler", "mobile engineer"},
	{"embedded", "firmware", "embedded softwareentwickler"},
	{"database administrator", "dba", "datenbankadministrator"},
	{"solutions architect", "software architect", "it architekt", "cloud architect", "softwarearchitekt"},
	{"engineering manager", "head of engineering", "teamleiter entwicklung", "team lead"},
}

// skill is a skill with its spellings; weight is how much it says about a role: 1 for common tools, 3 for
// specialised ones.
type skill struct {
	name    string
	weight  int
	aliases []string
}

var skills = []skill{
	{"Go", 3, []string{"golang", "go"}},
	{"Rust", 3, []string{"rust"}},
	{"Java", 2, []string{"java"}},
	{"Kotlin", 2, []string{"kotlin"}},
	{"Scala", 3, []string{"scala"}},
	{"Python", 2, []string{"python"}},
	{"JavaScript", 2, []string{"javascript", "ecmascript"}},
	{"TypeScript", 2, []string{"typescript"}},
	{"C++", 3, []string{"cpp"}},
	{"C#", 2, []string{"csharp"}},
	{".NET", 2, []string{"dotnet", "asp net"}},
	{"PHP", 2, []string{"php"}},
	{"Ruby", 2, []string{"ruby", "rails"}},
	{"Swift", 3, []string{"swift"}},
	{"Elixir", 3, []string{"elixir"}},
	{"Bash", 1, []string{"bash", "shell scripting"}},
	{"PowerShell", 2, []string{"powershell"}},
	{"SQL", 1, []string{"sql"}},
	{"PostgreSQL", 2, []string{"postgresql", "postgres"}},
	{"MySQL", 2, []string{"mysql", "mariadb"}},
	{"MongoDB", 2, []string{"mongodb"}},
	{"Redis", 2, []string{"redis"}},
	{"Elasticsearch", 2, []string{"elasticsearch", "opensearch"}},
	{"Kafka", 3, []string{"kafka"}},
	{"RabbitMQ", 2, []string{"rabbitmq"}},
	{"React", 2, []string{"react"}},
	{"Angular", 2, []string{"angular"}},
	{"Vue", 2, []string{"vue"}},
	{"Node.js", 2, []string{"nodejs"}},
	{"Spring", 2, []string{"spring boot", "spring"}},
	{"Django", 2, []string{"django"}},
	{"GraphQL", 2, []string{"graphql"}},
	{"gRPC", 3, []string{"grpc", "protobuf"}},
	{"REST", 1, []string{"rest api", "restful"}},
	{"Docker", 2, []string{"docker", "container"}},
	{"Kubernetes", 3, []string{"kubernetes", "k8s", "openshift"}},
	{"Helm", 2, []string{"helm"}},
	{"Terraform", 3, []string{"terraform", "opentofu"}},
	{"Ansible", 2, []string{"ansible"}},
	{"AWS", 2, []string{"aws", "amazon web services"}},
	{"Azure", 2, []string{"azure"}},
	{"GCP", 2, []string{"gcp", "google cloud"}},
	{"Linux", 1, []string{"linux", "unix"}},
	{"Windows Server", 1, []string{"windows server", "active directory"}},
	{"Networking", 2, []string{"tcp ip", "networking", "netzwerktechnik", "dns", "vpn"}},
	{"Prometheus", 2, []string{"prometheus", "grafana"}},
	{"OpenTelemetry", 3, []string{"opentelemetry", "otel"}},
	{"CI/CD", 1, []string{"cicd", "github actions", "gitlab ci", "jenkins"}},
	{"Git", 1, []string{"git"}},
	{"Microservices", 2, []string{"microservices", "microservice"}},
	{"Machine Learning", 3, []string{"machine learning", "pytorch", "tensorflow"}},
	{"Spark", 3, []string{"spark", "databricks"}},
	{"Security", 2, []string{"iso 27001", "owasp", "siem", "penetration testing"}},
	{"Agile", 1, []string{"scrum", "agile", "kanban"}},
	{"SAP", 2, []string{"sap", "abap"}},
	{"ITIL", 1, []string{"itil"}},
}

// regionAliases names a remote region in the ways postings write it; a remote job in a wider region (Europe)
// counts for a country in it.
var regionAliases = map[string][]string{
	"germany":     {"germany", "deutschland", "dach", "de", "europe", "europa", "eu", "emea", "cet", "cest"},
	"austria":     {"austria", "oesterreich", "dach", "europe", "europa", "eu", "emea", "cet"},
	"switzerland": {"switzerland", "schweiz", "dach", "europe", "europa", "emea", "cet"},
	"dach":        {"dach", "germany", "deutschland", "austria", "oesterreich", "switzerland", "schweiz"},
	"eu":          {"eu", "europe", "europa", "emea", "european union"},
	"europe":      {"europe", "europa", "eu", "emea"},
}

// everywhere are remote locations that cover every region.
var everywhere = terms([]string{"anywhere", "worldwide", "weltweit", "global", "remote first"})

// remoteWords mark a location as remote.
var remoteWords = terms([]string{"remote", "home office", "homeoffice", "work from home", "mobiles arbeiten",
	"telearbeit", "fully remote", "distributed"})

// generic title words say nothing about the role on their own.
var generic = map[string]bool{"engineer": true, "developer": true, "entwickler": true, "manager": true,
	"specialist": true, "spezialist": true, "mitarbeiter": true, "consultant": true, "berater": true, "and": true,
	"und": true, "the": true, "for": true, "fuer": true, "with": true, "mit": true, "senior": true, "junior": true,
	"lead": true, "staff": true, "principal": true, "head": true, "intern": true, "werkstudent": true,
	"praktikant": true, "remote": true}
