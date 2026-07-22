import { createContext, type ReactNode, useContext } from "react";

type WorkspaceEmbeddedPage = {
  readonly actionsHost: Element | null;
};

const WorkspaceEmbeddedPageContext =
  createContext<WorkspaceEmbeddedPage | null>(null);

export function WorkspaceEmbeddedPageProvider({
  actionsHost,
  children,
}: {
  readonly actionsHost: Element | null;
  readonly children: ReactNode;
}) {
  return (
    <WorkspaceEmbeddedPageContext.Provider value={{ actionsHost }}>
      {children}
    </WorkspaceEmbeddedPageContext.Provider>
  );
}

export function useWorkspaceEmbeddedPage() {
  return useContext(WorkspaceEmbeddedPageContext);
}
