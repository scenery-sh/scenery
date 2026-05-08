env "dev" {
  src = [
    "file://auth/db/schema.hcl",
  ]

  url = format(
    "postgres://postgres:postgres@127.0.0.1:%s/postgres?sslmode=disable",
    getenv("POSTGRES_PORT") != "" ? getenv("POSTGRES_PORT") : "5433",
  )

  dev = "docker://postgres/18/dev"
}
