/**
 * Custom error types for the KektorDB client.
 */

export class KektorDBError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "KektorDBError";
  }
}

export class APIError extends KektorDBError {
  public readonly statusCode: number;

  constructor(message: string, statusCode: number) {
    super(message);
    this.name = "APIError";
    this.statusCode = statusCode;
  }
}

export class ConnectionError extends KektorDBError {
  constructor(message: string) {
    super(message);
    this.name = "ConnectionError";
  }
}

export class TimeoutError extends KektorDBError {
  constructor(message: string) {
    super(message);
    this.name = "TimeoutError";
  }
}
