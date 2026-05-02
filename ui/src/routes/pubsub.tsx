import { Link } from "@tanstack/react-router";
import { useEffect, useMemo, useState } from "react";
import { useDashboard } from "../lib/dashboard-context";
import { cn } from "../lib/utils";
import {
  InlineStat,
  QueueSparkline,
  PubSubMessageRow,
  TopicCard,
  buildChartPoints,
  compareMessages,
  formatMillis,
  formatNumber,
  findSubscription,
  mergeLiveSnapshot,
  messageToAttempt,
  messageRowKey,
  messageWithinFilters,
  periodToMs,
  queueID,
  summarize,
  upsertAttempt,
  upsertMessage,
} from "./pubsub-components";
import type {
  PubSubHistoryPoint,
  PubSubMessage,
  PubSubMessageAttempt,
  PubSubMessagesResponse,
  PubSubSnapshot,
  PubSubSubscription,
  PubSubTopic,
} from "../lib/types";

const chartPeriods = [
  { label: "5m", value: "5m" },
  { label: "15m", value: "15m" },
  { label: "1h", value: "1h" },
  { label: "6h", value: "6h" },
  { label: "24h", value: "24h" },
] as const;

export function PubSubPage() {
  const { appId, pubsub, rpc } = useDashboard();
  const [period, setPeriod] = useState<(typeof chartPeriods)[number]["value"]>("15m");
  const [periodSnapshot, setPeriodSnapshot] = useState<PubSubSnapshot | null>(null);
  const [messages, setMessages] = useState<PubSubMessage[]>([]);
  const [messagesLoading, setMessagesLoading] = useState(false);
  const [messagesError, setMessagesError] = useState<string | null>(null);
  const [viewMode, setViewMode] = useState<"messages" | "dlq">("messages");
  const [expandedRow, setExpandedRow] = useState<string | null>(null);
  const [attemptsByRow, setAttemptsByRow] = useState<Record<string, PubSubMessageAttempt[]>>({});
  const [topicFilter, setTopicFilter] = useState("");
  const [queueFilter, setQueueFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState("");
  const [clearing, setClearing] = useState(false);
  const [clearError, setClearError] = useState<string | null>(null);
  const activeSnapshot = periodSnapshot ?? pubsub;
  const topics = activeSnapshot?.topics ?? [];
  const totals = useMemo(() => summarize(topics), [topics]);
  const availableTopics = useMemo(
    () => Array.from(new Set([...topics.map((item) => item.name), ...messages.map((item) => item.topic_name)])).sort(),
    [messages, topics],
  );
  const availableQueues = useMemo(() => {
    const names = new Set<string>();
    for (const topic of topics) {
      if (topicFilter && topic.name !== topicFilter) {
        continue;
      }
      for (const sub of topic.subscriptions) {
        names.add(sub.name);
      }
    }
    for (const message of messages) {
      if (topicFilter && message.topic_name !== topicFilter) {
        continue;
      }
      names.add(message.subscription_name);
    }
    return Array.from(names).sort();
  }, [messages, topicFilter, topics]);

  useEffect(() => {
    let cancelled = false;
    async function refreshPeriod() {
      if (!rpc) {
        return;
      }
      const next = await rpc.request<PubSubSnapshot>("pubsub/status", { app_id: appId, period });
      if (!cancelled) {
        setPeriodSnapshot(next);
      }
    }
    void refreshPeriod().catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [appId, period, rpc]);

  useEffect(() => {
    let cancelled = false;
    async function refreshMessages() {
      if (!rpc) {
        return;
      }
      setMessagesLoading(true);
      try {
        const next = await rpc.request<PubSubMessagesResponse>("pubsub/messages", {
          app_id: appId,
          period,
          topic_name: topicFilter,
          queue_name: queueFilter,
          status: viewMode === "dlq" ? "dead_lettered" : statusFilter,
          limit: 500,
        });
        if (!cancelled) {
          setMessages(next.messages ?? []);
          setMessagesError(null);
        }
      } catch (err) {
        if (!cancelled) {
          setMessagesError(err instanceof Error ? err.message : String(err));
        }
      } finally {
        if (!cancelled) {
          setMessagesLoading(false);
        }
      }
    }
    void refreshMessages();
    return () => {
      cancelled = true;
    };
  }, [appId, period, queueFilter, rpc, statusFilter, topicFilter, viewMode]);

  useEffect(() => {
    if (!rpc) {
      return;
    }
    const unsubscribe = rpc.subscribe((notification) => {
      if (notification.method === "pubsub/update") {
        const params = notification.params as PubSubSnapshot;
        if (params.app_id === appId) {
          setPeriodSnapshot((current) => mergeLiveSnapshot(current, params));
        }
      }
      if (notification.method === "pubsub/message") {
        const message = notification.params as PubSubMessage;
        if (message.app_id !== appId) {
          return;
        }
        if (messageWithinFilters(message, period, topicFilter, queueFilter, viewMode === "dlq" ? "dead_lettered" : statusFilter)) {
          setMessages((current) => upsertMessage(current, message));
        } else {
          setMessages((current) => current.filter((item) => messageRowKey(item) !== messageRowKey(message)));
        }
        if (message.attempt && message.attempt > 0) {
          setAttemptsByRow((current) => {
            const key = messageRowKey(message);
            const existing = current[key];
            if (!existing) {
              return current;
            }
            const attempt = messageToAttempt(message);
            return {
              ...current,
              [key]: upsertAttempt(existing, attempt),
            };
          });
        }
      }
      if (notification.method === "pubsub/messages/cleared") {
        const params = notification.params as { app_id?: string };
        if (params.app_id === appId) {
          setMessages((current) =>
            current.map((message) =>
              message.status === "queued" || message.status === "processing" || message.status === "retrying"
                ? {
                    ...message,
                    status: "cleared",
                    result: { status: "cleared" },
                    finished_at: new Date().toISOString(),
                  }
                : message,
            ),
          );
        }
      }
    });
    return unsubscribe;
  }, [appId, period, queueFilter, rpc, statusFilter, topicFilter, viewMode]);

  useEffect(() => {
    if (!pubsub) {
      return;
    }
    setPeriodSnapshot((current) => mergeLiveSnapshot(current, pubsub));
  }, [pubsub]);

  useEffect(() => {
    if (topicFilter && !availableTopics.includes(topicFilter)) {
      setTopicFilter("");
    }
  }, [availableTopics, topicFilter]);

  useEffect(() => {
    if (queueFilter && !availableQueues.includes(queueFilter)) {
      setQueueFilter("");
    }
  }, [availableQueues, queueFilter]);

  useEffect(() => {
    if (!rpc || !expandedRow) {
      return;
    }
    const rpcClient = rpc;
    const rowKey = expandedRow;
    const target = messages.find((item) => messageRowKey(item) === rowKey);
    if (!target) {
      return;
    }
    const targetMessage = target;
    let cancelled = false;
    async function loadAttempts() {
      const next = await rpcClient.request<{ attempts: PubSubMessageAttempt[] }>("pubsub/message/attempts", {
        app_id: appId,
        message_id: targetMessage.message_id,
        subscription_name: targetMessage.subscription_name,
      });
      if (!cancelled) {
        setAttemptsByRow((current) => ({
          ...current,
          [rowKey]: next.attempts ?? [],
        }));
      }
    }
    void loadAttempts().catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [appId, expandedRow, messages, rpc]);

  async function clearQueues() {
    if (!rpc || clearing) {
      return;
    }
    const confirmed = window.confirm(
      "Clear all queued Pub/Sub jobs from the local embedded NATS runtime? In-flight handlers may continue running.",
    );
    if (!confirmed) {
      return;
    }
    setClearing(true);
    setClearError(null);
    try {
      const next = await rpc.request<PubSubSnapshot>("pubsub/clear", { app_id: appId });
      setPeriodSnapshot(next);
    } catch (err) {
      setClearError(err instanceof Error ? err.message : String(err));
    } finally {
      setClearing(false);
    }
  }

  return (
    <div className="max-h-[calc(100vh-var(--header-height))] overflow-auto">
      <div className="min-h-0 grow px-8 pt-6 pb-12 leading-6">
        <div className="max-w-7xl space-y-8">
          <div className="flex items-start justify-between gap-4">
            <div>
              <h1 className="text-lg font-medium">Pub/Sub</h1>
              <p className="mt-2 max-w-3xl text-sm text-muted-foreground">
                Live local queue and worker metrics from onlava&apos;s embedded NATS runtime.
              </p>
            </div>
            <div className="flex flex-col items-end gap-3">
              <div className="text-right text-xs text-muted-foreground">
                <div>Last update</div>
                <div className="mt-1 text-foreground">
                  {pubsub?.updated_at ? new Date(pubsub.updated_at).toLocaleTimeString() : "none"}
                </div>
              </div>
              <button
                type="button"
                onClick={() => void clearQueues()}
                disabled={!rpc || clearing || topics.length === 0}
                className="rounded-md border border-red-950/80 bg-red-950/20 px-3 py-1.5 text-xs font-medium text-red-300 hover:border-red-700 hover:text-red-200 disabled:cursor-not-allowed disabled:opacity-40"
              >
                {clearing ? "Clearing..." : "Clear queued jobs"}
              </button>
              <div className="flex rounded-md border border-border bg-sidebar/60 p-1">
                {chartPeriods.map((item) => (
                  <button
                    key={item.value}
                    type="button"
                    onClick={() => setPeriod(item.value)}
                    className={
                      item.value === period
                        ? "rounded-sm bg-foreground px-3 py-1 text-xs font-medium text-background"
                        : "rounded-sm px-3 py-1 text-xs text-muted-foreground hover:text-foreground"
                    }
                  >
                    {item.label}
                  </button>
                ))}
              </div>
            </div>
          </div>

          <div className="flex flex-wrap items-center gap-x-8 gap-y-3 rounded-md border border-border bg-sidebar/20 px-4 py-3">
            <InlineStat label="Topics" value={String(topics.length)} />
            <InlineStat label="Subscriptions" value={String(totals.subscriptions)} />
            <InlineStat label="Queued" value={formatNumber(totals.pending)} />
            <InlineStat label="Picked up" value={formatNumber(totals.pickedUp)} />
            <InlineStat label="In flight" value={formatNumber(totals.inFlight)} />
            <InlineStat label="Avg job" value={formatMillis(totals.avgDurationMs)} />
          </div>
          {clearError ? <div className="text-sm text-red-400">{clearError}</div> : null}

          {topics.length === 0 ? (
            <div className="rounded-md border border-border p-6 text-sm text-muted-foreground">
              No Pub/Sub topics have been reported yet. Start the app with packages that define{" "}
              <code>pubsub.NewTopic</code> and publish or receive messages to populate live metrics.
            </div>
          ) : (
            <div className="space-y-6">
              {topics.map((topic) => (
                <TopicCard
                  key={topic.name}
                  topic={topic}
                  history={activeSnapshot?.history ?? []}
                  latest={activeSnapshot}
                  period={period}
                />
              ))}
            </div>
          )}

          <section className="rounded-md border border-border p-6">
            <div className="flex flex-wrap items-start justify-between gap-4">
              <div>
                <div className="flex items-center gap-2">
                  <button
                    type="button"
                    onClick={() => setViewMode("messages")}
                    className={
                      viewMode === "messages"
                        ? "rounded-md bg-foreground px-3 py-1.5 text-sm font-medium text-background"
                        : "rounded-md border border-border px-3 py-1.5 text-sm text-muted-foreground hover:text-foreground"
                    }
                  >
                    Messages
                  </button>
                  <button
                    type="button"
                    onClick={() => setViewMode("dlq")}
                    className={
                      viewMode === "dlq"
                        ? "rounded-md bg-foreground px-3 py-1.5 text-sm font-medium text-background"
                        : "rounded-md border border-border px-3 py-1.5 text-sm text-muted-foreground hover:text-foreground"
                    }
                  >
                    DLQ
                  </button>
                </div>
                <p className="mt-2 max-w-3xl text-sm text-muted-foreground">
                  {viewMode === "dlq"
                    ? "Dead-lettered jobs for local queues, including failure details and attempt history."
                    : "Recent jobs submitted to local queues, with per-queue status, timing, payload, and result details."}
                </p>
              </div>
              <div className="text-right text-xs text-muted-foreground">
                <div>Window</div>
                <div className="mt-1 text-foreground">{period}</div>
              </div>
            </div>

            <div className="mt-5 grid gap-3 md:grid-cols-4">
              <label className="text-xs text-muted-foreground">
                <span className="mb-2 block uppercase tracking-wide">Queue</span>
                <select
                  value={queueFilter}
                  onChange={(event) => setQueueFilter(event.target.value)}
                  className="w-full rounded-md border border-border bg-sidebar/40 px-3 py-2 text-sm text-foreground outline-none"
                >
                  <option value="">All queues</option>
                  {availableQueues.map((queue) => (
                    <option key={queue} value={queue}>
                      {queue}
                    </option>
                  ))}
                </select>
              </label>
              <label className="text-xs text-muted-foreground">
                <span className="mb-2 block uppercase tracking-wide">Topic</span>
                <select
                  value={topicFilter}
                  onChange={(event) => setTopicFilter(event.target.value)}
                  className="w-full rounded-md border border-border bg-sidebar/40 px-3 py-2 text-sm text-foreground outline-none"
                >
                  <option value="">All topics</option>
                  {availableTopics.map((topic) => (
                    <option key={topic} value={topic}>
                      {topic}
                    </option>
                  ))}
                </select>
              </label>
              <label className="text-xs text-muted-foreground">
                <span className="mb-2 block uppercase tracking-wide">Status</span>
                <select
                  value={statusFilter}
                  onChange={(event) => setStatusFilter(event.target.value)}
                  disabled={viewMode === "dlq"}
                  className="w-full rounded-md border border-border bg-sidebar/40 px-3 py-2 text-sm text-foreground outline-none"
                >
                  <option value="">All statuses</option>
                  <option value="queued">Queued</option>
                  <option value="processing">Processing</option>
                  <option value="retrying">Retrying</option>
                  <option value="completed">Completed</option>
                  <option value="dead_lettered">Dead lettered</option>
                  <option value="cleared">Cleared</option>
                </select>
              </label>
              <div className="flex items-end">
                <div className="w-full rounded-md border border-border bg-sidebar/20 px-3 py-2 text-sm">
                  <span className="text-muted-foreground">Rows</span>
                  <span className="ml-2 font-medium tabular-nums">{formatNumber(messages.length)}</span>
                </div>
              </div>
            </div>

            {messagesError ? <div className="mt-4 text-sm text-red-400">{messagesError}</div> : null}

            <div className="mt-5 overflow-hidden rounded-md border border-border">
              <table className="w-full text-sm">
                <thead className="bg-sidebar/60 text-xs uppercase tracking-wide text-muted-foreground">
                  <tr>
                    <th className="px-4 py-3 text-left font-medium">Queue</th>
                    <th className="px-4 py-3 text-left font-medium">Status</th>
                    <th className="px-4 py-3 text-left font-medium">Inserted</th>
                    <th className="px-4 py-3 text-left font-medium">Picked up</th>
                    <th className="px-4 py-3 text-left font-medium">Finished</th>
                    <th className="px-4 py-3 text-right font-medium">Duration</th>
                    <th className="px-4 py-3 text-left font-medium">Input</th>
                    <th className="px-4 py-3 text-left font-medium">Output</th>
                  </tr>
                </thead>
                <tbody>
                  {messagesLoading && messages.length === 0 ? (
                    <tr>
                      <td colSpan={8} className="px-4 py-8 text-center text-muted-foreground">
                        Loading queue messages…
                      </td>
                    </tr>
                  ) : messages.length === 0 ? (
                    <tr>
                      <td colSpan={8} className="px-4 py-8 text-center text-muted-foreground">
                        {viewMode === "dlq"
                          ? "No dead-lettered queue messages found for the selected filters and timeframe."
                          : "No queue messages found for the selected filters and timeframe."}
                      </td>
                    </tr>
                  ) : (
                    messages.map((message) => (
                      <PubSubMessageRow
                        key={messageRowKey(message)}
                        message={message}
                        open={expandedRow === messageRowKey(message)}
                        attempts={attemptsByRow[messageRowKey(message)] ?? []}
                        onToggle={() =>
                          setExpandedRow((current) => (current === messageRowKey(message) ? null : messageRowKey(message)))
                        }
                      />
                    ))
                  )}
                </tbody>
              </table>
            </div>
          </section>
        </div>
      </div>
    </div>
  );
}
