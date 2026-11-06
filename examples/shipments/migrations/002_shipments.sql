create table shipments (
    id           uuid primary key,
    owner_id     uuid not null references users (id) on delete cascade,
    reference    text not null,
    destination  text not null,
    weight_kg    numeric(10, 2) not null check (weight_kg > 0),
    status       text not null default 'Booked',
    carrier      text,
    eta          date,
    delivered_on date,
    reason       text,
    created_at   timestamptz not null default now()
);

create index shipments_owner on shipments (owner_id, created_at desc);
