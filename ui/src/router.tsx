import { createRootRoute, createRoute, createRouter, Navigate, Outlet } from "@tanstack/react-router";
import { DashboardRouteShell } from "./components/layout";
import { AppListPage } from "./routes/app-list";
import { RequestsPage } from "./routes/requests";
import { TracesListPage, TracesPage } from "./routes/traces";
import { ApiPage } from "./routes/api";
import { ServicesPage } from "./routes/services";
import { DatabasePage } from "./routes/db";
import { CronPage } from "./routes/cron";

const rootRoute = createRootRoute({
  component: () => <Outlet />,
});

const landingRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  component: AppListPage,
});

const appRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/$appId",
  component: function AppRoute() {
    const { appId } = appRoute.useParams();
    return <DashboardRouteShell appId={appId} />;
  },
});

const appIndexRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/",
  component: function AppIndexRedirect() {
    const { appId } = appRoute.useParams();
    return <Navigate to="/$appId/requests" params={{ appId }} replace />;
  },
});

const requestsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "requests",
  component: RequestsPage,
});

const serviceCatalogRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "envs/local/api",
  component: ServicesPage,
});

const serviceCatalogServiceRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "envs/local/api/$serviceSlug",
  component: ServicesPage,
});

const serviceCatalogRPCRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "envs/local/api/$serviceSlug/$rpcSlug",
  component: ApiPage,
});

const tracesIndexRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "envs/local/traces",
  component: TracesListPage,
});

const traceDetailRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "envs/local/traces/$traceId",
  component: function TraceDetailRoute() {
    const { traceId } = traceDetailRoute.useParams();
    return <TracesPage traceId={traceId} />;
  },
});

const traceSpanDetailRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "envs/local/traces/$traceId/$spanId",
  component: function TraceSpanDetailRoute() {
    const { traceId, spanId } = traceSpanDetailRoute.useParams();
    return <TracesPage traceId={traceId} spanId={spanId} />;
  },
});

const dbRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "db",
  component: DatabasePage,
});

const dbDetailRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "db/$dbSlug",
  component: DatabasePage,
});

const cronRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "cron",
  component: CronPage,
});

const legacyTracesRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "traces",
  component: function LegacyTracesRoute() {
    const { appId } = appRoute.useParams();
    return <Navigate to="/$appId/envs/local/traces" params={{ appId }} replace />;
  },
});

const legacyTraceDetailRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "traces/$traceId",
  component: function LegacyTraceDetailRoute() {
    const { appId, traceId } = legacyTraceDetailRoute.useParams();
    return <Navigate to="/$appId/envs/local/traces/$traceId" params={{ appId, traceId }} replace />;
  },
});

const legacyServicesRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "services",
  component: function LegacyServicesRoute() {
    const { appId } = appRoute.useParams();
    return <Navigate to="/$appId/envs/local/api" params={{ appId }} replace />;
  },
});

const legacyApiRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "api",
  component: function LegacyApiRoute() {
    const { appId } = appRoute.useParams();
    return <Navigate to="/$appId/requests" params={{ appId }} replace />;
  },
});

const routeTree = rootRoute.addChildren([
  landingRoute,
  appRoute.addChildren([
    appIndexRoute,
    requestsRoute,
    serviceCatalogRoute,
    serviceCatalogServiceRoute,
    serviceCatalogRPCRoute,
    tracesIndexRoute,
    traceDetailRoute,
    traceSpanDetailRoute,
    dbRoute,
    dbDetailRoute,
    cronRoute,
    legacyTracesRoute,
    legacyTraceDetailRoute,
    legacyServicesRoute,
    legacyApiRoute,
  ]),
]);

export const router = createRouter({ routeTree });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
