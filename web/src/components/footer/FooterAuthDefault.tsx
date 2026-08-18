/*eslint-disable*/
export default function Footer() {
  return (
    <div className="z-[5] mx-auto flex w-full max-w-screen-sm flex-col items-center justify-between px-[20px] pb-4 lg:mb-6 lg:max-w-[100%] lg:flex-row xl:mb-2 xl:w-[1310px] xl:pb-6">
      <p className="mb-6 text-center text-sm text-gray-600 md:text-base lg:mb-0">
        ©{new Date().getFullYear()} Horizon UI. All Rights Reserved by <a href="https://github.com/horizon-ui" className="text-green-600 hover:text-green-700 font-semibold" target="_blank" rel="noopener noreferrer">Horizon UI</a> • Distributed by <a href="https://themewagon.com" className="text-green-600 hover:text-green-700 font-semibold" target="_blank" rel="noopener noreferrer">ThemeWagon</a>
      </p>
      <ul className="flex flex-wrap items-center sm:flex-nowrap">
        <li className="mr-12">
          <a
            target="blank"
            href="mailto:hello@simmmple.com"
            className="text-sm text-gray-600 hover:text-gray-600 md:text-base lg:text-white lg:hover:text-white"
          >
            Support
          </a>
        </li>
        <li className="mr-12">
          <a
            href="#"
            className="text-sm text-gray-600 hover:text-gray-600 md:text-base lg:text-white lg:hover:text-white"
          >
            License
          </a>
        </li>
        <li className="mr-12">
          <a
            href="#"
            className="text-sm text-gray-600 hover:text-gray-600 md:text-base lg:text-white lg:hover:text-white"
          >
            Terms of Use
          </a>
        </li>
        <li>
          <a
            href="#"
            className="text-sm text-gray-600 hover:text-gray-600 md:text-base lg:text-white lg:hover:text-white"
          >
            Blog
          </a>
        </li>
      </ul>
    </div>
  );
}
