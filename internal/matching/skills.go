package matching

import "github.com/1xxn/getafjob/internal/platform/vocab"

var skills = vocab.Skills

// implied are the skills a skill brings along: who knows a tool knows the concept or language under it (PyTorch
// means machine learning and Python), never the other way round.
var implied = map[string][]string{
	"TypeScript": {"JavaScript"}, "React": {"JavaScript"}, "Next.js": {"React", "JavaScript"},
	"Angular": {"TypeScript", "JavaScript"}, "Vue": {"JavaScript"}, "Svelte": {"JavaScript"},
	"Node.js": {"JavaScript"}, "Express": {"Node.js", "JavaScript"}, "NestJS": {"Node.js", "TypeScript"},
	"React Native": {"React", "JavaScript"}, "Flutter": {"Dart"}, "Tailwind CSS": {"CSS"},
	"Spring": {"Java"}, "Quarkus": {"Java"}, "Jakarta EE": {"Java"}, "Hibernate": {"Java", "SQL"},
	"Django": {"Python"}, "Flask": {"Python"}, "FastAPI": {"Python"}, "Pandas": {"Python"}, "NumPy": {"Python"},
	"Ruby on Rails": {"Ruby"}, "Laravel": {"PHP"}, "Symfony": {"PHP"}, "Phoenix": {"Elixir"},
	"PostgreSQL": {"SQL"}, "MySQL": {"SQL"}, "Oracle Database": {"SQL"}, "SQL Server": {"SQL"}, "SQLite": {"SQL"},
	"Snowflake": {"SQL"}, "BigQuery": {"SQL"}, "dbt": {"SQL", "ETL"}, "ClickHouse": {"SQL"},
	"scikit-learn": {"Machine Learning", "Python"}, "PyTorch": {"Machine Learning", "Python"},
	"TensorFlow": {"Machine Learning", "Python"}, "MLOps": {"Machine Learning"}, "NLP": {"Machine Learning"},
	"Computer Vision": {"Machine Learning"}, "LLM": {"Machine Learning", "NLP"},
	"Spark": {"ETL"}, "Databricks": {"Spark", "ETL"}, "Airflow": {"ETL", "Python"}, "Flink": {"ETL"},
	"Kubernetes": {"Docker"}, "Helm": {"Kubernetes", "Docker"}, "Argo CD": {"Kubernetes", "CI/CD"},
	"Istio": {"Kubernetes"}, "Pulumi": {}, "Bicep": {"Azure"}, "CloudFormation": {"AWS"},
	"GitHub Actions": {"CI/CD", "Git"}, "GitLab CI": {"CI/CD", "Git"}, "Jenkins": {"CI/CD"},
	"Grafana": {"Prometheus"}, "Prometheus": {"Grafana"}, "DevSecOps": {"Security", "CI/CD"},
	"Penetration testing": {"Security"}, "SIEM": {"Security"}, "ISO 27001": {"Security"}, "OWASP": {"Security"},
	"End-to-end testing": {"Test automation"}, "Performance testing": {"Test automation"},
}
