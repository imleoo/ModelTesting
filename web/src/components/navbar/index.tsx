import React from 'react';
import { FiAlignJustify } from 'react-icons/fi';
import { RiMoonFill, RiSunFill } from 'react-icons/ri';
import NavLink from 'components/link/NavLink';

const Navbar = (props: {
  onOpenSidenav: () => void;
  brandText: string;
  secondary?: boolean | string;
  [x: string]: any;
}) => {
  const { onOpenSidenav, brandText } = props;
  const [darkmode, setDarkmode] = React.useState(
    document.body.classList.contains('dark'),
  );
  return (
    <nav className="sticky top-4 z-40 flex flex-row flex-wrap items-center justify-between rounded-xl bg-white/10 p-2 backdrop-blur-xl dark:bg-[#0b14374d]">
      <div className="ml-[6px]">
        <div className="h-6 pt-1">
          <NavLink
            className="text-sm font-normal text-navy-700 hover:underline dark:text-white dark:hover:text-white"
            href="/admin"
          >
            模型自测台
          </NavLink>
          <span className="mx-1 text-sm text-navy-700 dark:text-white">/</span>
          <span className="text-sm font-normal text-navy-700 dark:text-white">
            {brandText}
          </span>
        </div>
        <p className="shrink text-[33px] font-bold text-navy-700 dark:text-white">
          {brandText}
        </p>
      </div>

      <div className="relative mt-[3px] flex h-[61px] items-center gap-3 rounded-full bg-white px-4 py-2 shadow-xl shadow-shadow-500 dark:!bg-navy-800 dark:shadow-none">
        <span
          className="flex cursor-pointer text-xl text-gray-600 dark:text-white xl:hidden"
          onClick={onOpenSidenav}
        >
          <FiAlignJustify className="h-5 w-5" />
        </span>
        <button
          type="button"
          aria-label={darkmode ? '切换为浅色模式' : '切换为深色模式'}
          className="cursor-pointer text-gray-600"
          onClick={() => {
            if (darkmode) {
              document.body.classList.remove('dark');
              setDarkmode(false);
            } else {
              document.body.classList.add('dark');
              setDarkmode(true);
            }
          }}
        >
          {darkmode ? (
            <RiSunFill className="h-4 w-4 text-gray-600 dark:text-white" />
          ) : (
            <RiMoonFill className="h-4 w-4 text-gray-600 dark:text-white" />
          )}
        </button>
      </div>
    </nav>
  );
};

export default Navbar;
