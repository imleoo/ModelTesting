import { HiX } from 'react-icons/hi';
import Links from './components/Links';

import { IRoute } from 'types/navigation';

function Sidebar(props: {
  routes: IRoute[];
  open: boolean;
  setOpen: (open: boolean) => void;
  [x: string]: any;
}) {
  const { routes, open, setOpen } = props;
  return (
    <div
      className={`sm:none duration-175 linear fixed !z-50 flex min-h-full flex-col bg-white pb-10 shadow-2xl shadow-white/5 transition-all dark:!bg-navy-800 dark:text-white md:!z-50 lg:!z-50 xl:!z-0 ${
        open ? 'translate-x-0' : '-translate-x-96 xl:translate-x-0'
      }`}
    >
      <span
        className="absolute right-4 top-4 block cursor-pointer xl:hidden"
        onClick={() => setOpen(false)}
      >
        <HiX />
      </span>

      <div className="mx-[56px] mt-[50px] flex flex-col">
        <div className="text-[26px] font-bold leading-tight text-navy-700 dark:text-white">
          模型自测台
        </div>
        <div className="mt-1 text-xs font-medium uppercase tracking-wider text-gray-600">
          Model Testbed
        </div>
      </div>
      <div className="mb-7 mt-[40px] h-px bg-gray-300 dark:bg-white/30" />

      <ul className="mb-auto pt-1">
        <Links routes={routes} />
      </ul>
    </div>
  );
}

export default Sidebar;
