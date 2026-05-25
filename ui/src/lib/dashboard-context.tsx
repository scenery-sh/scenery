import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { DashboardRpcClient } from "./rpc";
import {
  bodyTextFromApiCall,
  dashboardWebsocketURL,
  processOutputText,
  upsertTrace,
} from "./utils";
import type {
  ApiCallResponse,
  APIEncoding,
  AppStatus,
  AppSummary,
  DashboardMeta,
  DashboardNotification,
  ProcessOutput,
  TraceSummary,
} from "./types";

interface DashboardContextValue {
  appId: string;
  connected: boolean;
  apps: AppSummary[];
  status: AppStatus | null;
  meta: DashboardMeta | null;
  apiEncoding: APIEncoding | null;
  traces: TraceSummary[];
  outputs: ProcessOutput[];
  rpc: DashboardRpcClient | null;
  refreshAll: () => Promise<void>;
  callAPI: (request: {
    service: string;
    endpoint: string;
    path: string;
    method: string;
    payload?: unknown;
    authToken?: string;
    correlationID?: string;
  }) => Promise<ApiCallResponse>;
}

const DashboardContext = createContext<DashboardContextValue | null>(null);

export function DashboardProvider({
  appId,
  children,
}: {
  appId: string;
  children: ReactNode;
}) {
  const clientRef = useRef<DashboardRpcClient | null>(null);
  if (!clientRef.current) {
    clientRef.current = new DashboardRpcClient(dashboardWebsocketURL());
  }

  const [connected, setConnected] = useState(false);
  const [apps, setApps] = useState<AppSummary[]>([]);
  const [status, setStatus] = useState<AppStatus | null>(null);
  const [traces, setTraces] = useState<TraceSummary[]>([]);
  const [outputs, setOutputs] = useState<ProcessOutput[]>([]);

  const refreshAll = useCallback(async () => {
    const rpc = clientRef.current;
    if (!rpc) {
      return;
    }
    const [nextApps, nextStatus, nextTraces, nextOutputs] = await Promise.all([
      rpc.request<AppSummary[]>("list-apps"),
      rpc.request<AppStatus>("status", { app_id: appId }),
      rpc.request<TraceSummary[]>("traces/list", { app_id: appId }),
      rpc.request<ProcessOutput[]>("process/output/list", { app_id: appId, limit: 300 }),
    ]);
    setApps(nextApps);
    setStatus(nextStatus);
    setTraces(nextTraces ?? []);
    setOutputs((nextOutputs ?? []).map((item) => ({
      ...item,
      output: processOutputText(item),
    })));
  }, [appId]);

  useEffect(() => {
    const rpc = clientRef.current;
    if (!rpc) {
      return;
    }
    rpc.connect();
    const unsubscribeConnection = rpc.subscribeConnection(setConnected);
    const unsubscribeNotifications = rpc.subscribe((notification: DashboardNotification) => {
      switch (notification.method) {
        case "process/start":
        case "process/reload":
        case "process/compile-start":
        case "process/compile-error":
        case "process/stop":
          setStatus(notification.params as AppStatus);
          void rpc.request<AppSummary[]>("list-apps").then(setApps).catch(() => undefined);
          break;
        case "grafana/status":
          setStatus((current) =>
            current
              ? { ...current, grafana: notification.params as AppStatus["grafana"] }
              : current,
          );
          break;
        case "process/output": {
          const next = notification.params as ProcessOutput;
          if (next.appID !== appId) {
            break;
          }
          setOutputs((current) => [
            ...current.slice(-299),
            { ...next, output: processOutputText(next) },
          ]);
          break;
        }
        case "trace/new": {
          const params = notification.params as { app_id?: string; span?: TraceSummary };
          if (params.app_id && params.app_id !== appId) {
            break;
          }
          if (params.span) {
            const span = params.span;
            setTraces((current) => upsertTrace(current, span));
          }
          break;
        }
        default:
          break;
      }
    });
    void refreshAll();
    const poll = window.setInterval(() => {
      void refreshAll().catch(() => undefined);
    }, 5000);
    return () => {
      unsubscribeConnection();
      unsubscribeNotifications();
      window.clearInterval(poll);
    };
  }, [appId, refreshAll]);

  useEffect(() => {
    void refreshAll().catch(() => undefined);
  }, [appId, refreshAll]);

  useEffect(() => {
    const rpc = clientRef.current;
    return () => {
      rpc?.dispose();
    };
  }, []);

  const value = useMemo<DashboardContextValue>(() => ({
    appId,
    connected,
    apps,
    status,
    meta: status?.meta ?? null,
    apiEncoding: status?.apiEncoding ?? null,
    traces,
    outputs,
    rpc: clientRef.current,
    refreshAll,
    callAPI: async ({ service, endpoint, path, method, payload, authToken, correlationID }) => {
      const rpc = clientRef.current;
      if (!rpc) {
        throw new Error("rpc not connected");
      }
      const result = await rpc.request<ApiCallResponse>("api-call", {
        app_id: appId,
        service,
        endpoint,
        path,
        method,
        payload: payload === undefined ? undefined : JSON.stringify(payload),
        auth_token: authToken ?? "",
        correlation_id: correlationID ?? "",
      });
      return {
        ...result,
        body: bodyTextFromApiCall(result.body),
      };
    },
  }), [appId, apps, connected, outputs, refreshAll, status, traces]);

  return <DashboardContext.Provider value={value}>{children}</DashboardContext.Provider>;
}

export function useDashboard(): DashboardContextValue {
  const value = useContext(DashboardContext);
  if (!value) {
    throw new Error("DashboardContext missing");
  }
  return value;
}
