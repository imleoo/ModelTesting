import React from 'react';
import { useCallback } from 'react';
import { usePathname } from 'next/navigation';
import NavLink from 'components/link/NavLink';
import { IRoute } from 'types/navigation';

export const SidebarLinks = (props: { routes: IRoute[] }): JSX.Element => {
  const pathname = usePathname();
  const { routes } = props;

  // 当前浏览器路径是否落在该路由下
  const activeRoute = useCallback(
    (routeName: string) => {
      return pathname?.includes(routeName);
    },
    [pathname],
  );

  return (
    <>
      {routes
        .filter((route) => !route.hidden)
        .map((route, index) => {
          const active = activeRoute(route.path) === true;
          return (
            <NavLink key={index} href={route.layout + '/' + route.path}>
              <div className="relative mb-3 flex hover:cursor-pointer">
                <li className="my-[3px] flex cursor-pointer items-center px-8">
                  <span
                    className={
                      active
                        ? 'font-bold text-brand-500 dark:text-white'
                        : 'font-medium text-gray-600'
                    }
                  >
                    {route.icon}
                  </span>
                  <p
                    className={`leading-1 ml-4 flex ${
                      active
                        ? 'font-bold text-navy-700 dark:text-white'
                        : 'font-medium text-gray-600'
                    }`}
                  >
                    {route.name}
                  </p>
                </li>
                {active ? (
                  <div className="absolute right-0 top-px h-9 w-1 rounded-lg bg-brand-500 dark:bg-brand-400" />
                ) : null}
              </div>
            </NavLink>
          );
        })}
    </>
  );
};

export default SidebarLinks;
