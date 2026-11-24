import { SQL } from "bun";

export function connectDatabase(url) {
  const database = new SQL(url);
  return {
    async probe() {
      const [row] = await database`SELECT 'postgres'::text AS vendor`;
      if (row.vendor !== "postgres") throw new Error("native database query returned an unexpected value");
    },
    close() {
      return database.close();
    },
  };
}
