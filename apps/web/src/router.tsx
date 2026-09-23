import { createRootRoute, createRoute, createRouter, type RouterHistory } from "@tanstack/react-router";
import { Layout } from "@/layout";
import { Blocks } from "@/routes/blocks";
import { Overview } from "@/routes/overview";
import { Sign } from "@/routes/sign";
import { Simulate } from "@/routes/simulate";

const root = createRootRoute({ component: Layout });
const routeTree = root.addChildren([
  createRoute({ getParentRoute: () => root, path: "/", component: Overview }),
  createRoute({ getParentRoute: () => root, path: "/blocks", component: Blocks }),
  createRoute({ getParentRoute: () => root, path: "/simulate", component: Simulate }),
  createRoute({ getParentRoute: () => root, path: "/sign", component: Sign }),
]);

export const makeRouter = (history?: RouterHistory) => createRouter({ routeTree, history });

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof makeRouter>;
  }
}
