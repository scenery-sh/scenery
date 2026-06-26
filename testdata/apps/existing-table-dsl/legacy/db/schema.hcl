schema "legacy" {}

table "legacy" "customers" {
  schema = schema.legacy

  column "id" {
    null = false
    type = text
  }

  column "email" {
    null = false
    type = text
  }

  column "full_name" {
    null = false
    type = text
  }

  column "plan_status" {
    null = false
    type = text
  }

  column "created_at" {
    null = false
    type = timestamptz
  }

  primary_key {
    columns = [column.id]
  }
}
