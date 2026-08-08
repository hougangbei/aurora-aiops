import { Dropdown, Spin, type MenuProps } from 'antd';

type ActionMenuButtonProps = {
  menu: MenuProps;
  loading?: boolean;
  className?: string;
};

export function ActionMenuButton({
  menu,
  loading = false,
  className,
}: ActionMenuButtonProps) {
  return (
    <div onClick={(event) => event.stopPropagation()}>
      <Dropdown trigger={['click']} menu={menu}>
        <button
          type="button"
          className={[
            'aurora-chip inline-flex h-8 items-center justify-center rounded-full border px-3 text-[13px] font-medium shadow-[0_1px_2px_rgba(15,23,42,0.04)] transition focus:outline-none',
            className ?? '',
          ].join(' ')}
          aria-label="操作"
        >
          <span>操作</span>
          {loading ? <span className="ml-2"><Spin size="small" /></span> : null}
        </button>
      </Dropdown>
    </div>
  );
}
