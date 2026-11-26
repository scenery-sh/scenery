import { SQL } from "bun";

export function connectDatabase(url) {
  const sql = new SQL(url);
  return {
    async updateEnrichment(slug, title, enrichedAt) {
      await sql`
        UPDATE shortlink.links
        SET title = ${title}, enriched_at = ${enrichedAt}
        WHERE slug = ${slug}
      `;
    },
    async latestClicks(afterId) {
      try {
        if (afterId === 0n) {
          const rows = await sql`
            SELECT id, click_id, slug, target_url, clicked_at
            FROM shortlink.clicks
            ORDER BY id DESC
            LIMIT 15
          `;
          return rows.reverse().map(mapRow);
        }
        const rows = await sql`
          SELECT id, click_id, slug, target_url, clicked_at
          FROM shortlink.clicks
          WHERE id > ${afterId.toString()}::bigint
          ORDER BY id ASC
          LIMIT 50
        `;
        return rows.map(mapRow);
      } catch (err) {
        // Table may not exist yet before the links service runs its schema DDL.
        if (String(err).includes("does not exist")) return [];
        throw err;
      }
    },
    close() {
      return sql.close();
    },
  };
}

function mapRow(row) {
  return {
    id: BigInt(row.id),
    clickId: String(row.click_id),
    slug: String(row.slug),
    targetUrl: String(row.target_url),
    clickedAt: row.clicked_at instanceof Date ? row.clicked_at.toISOString() : String(row.clicked_at),
  };
}
