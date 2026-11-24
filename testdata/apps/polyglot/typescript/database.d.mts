export function connectDatabase(url: string): {
  probe(): Promise<void>;
  close(): Promise<void>;
};
