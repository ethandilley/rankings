-- +goose Up
WITH owner_map (old_owner, new_owner) AS (
  VALUES
    ('Whats up my Nabers', 'hisrchel'),
    ('Krazy-Eyez Killa', 'johnny'),
    ('Threepeat is embarassing 💍💍💍', 'rohan'),
    ('Two Girls One Kittle', 'eric'),
    ('Nacua Matata', 'john'),
    ('Dat bih 💨', 'william'),
    ('don''t go breakin my dart', 'tony'),
    ('don’t go breakin my dart', 'tony'),
    ('Trade King', 'yash'),
    ('Denver on 3', 'vibhav'),
    ('Gay Flowers', 'ethan'),
    ('Drake Maye Lover', 'tommy'),
    ('Bark for me 🐶', 'anwar'),
    ('Hisrchel Nambiar', 'hisrchel'),
    ('Johnny Meshramkar', 'johnny'),
    ('Rohan Thandu', 'rohan'),
    ('Eric Ming', 'eric'),
    ('John Webster', 'john'),
    ('William Beard', 'william'),
    ('Tony Capiello', 'tony'),
    ('Yash Patel', 'yash'),
    ('Vibhav Kumar', 'vibhav'),
    ('Ethan Stone', 'ethan'),
    ('Ethan Dilley', 'ethan'),
    ('Tommy Zaffiro', 'tommy'),
    ('Anwar Adous', 'anwar'),
    ('Hisrchel', 'hisrchel'),
    ('Johnny', 'johnny'),
    ('Rohan', 'rohan'),
    ('Eric', 'eric'),
    ('John', 'john'),
    ('William', 'william'),
    ('Tony', 'tony'),
    ('Yash', 'yash'),
    ('Vibhav', 'vibhav'),
    ('Ethan', 'ethan'),
    ('Tommy', 'tommy'),
    ('Anwar', 'anwar')
)
UPDATE rankings r
SET owner = m.new_owner
FROM owner_map m
WHERE lower(trim(r.owner)) = lower(m.old_owner)
  AND r.owner <> m.new_owner;

WITH owner_map (old_owner, new_owner) AS (
  VALUES
    ('Whats up my Nabers', 'hisrchel'),
    ('Krazy-Eyez Killa', 'johnny'),
    ('Threepeat is embarassing 💍💍💍', 'rohan'),
    ('Two Girls One Kittle', 'eric'),
    ('Nacua Matata', 'john'),
    ('Dat bih 💨', 'william'),
    ('don''t go breakin my dart', 'tony'),
    ('don’t go breakin my dart', 'tony'),
    ('Trade King', 'yash'),
    ('Denver on 3', 'vibhav'),
    ('Gay Flowers', 'ethan'),
    ('Drake Maye Lover', 'tommy'),
    ('Bark for me 🐶', 'anwar'),
    ('Hisrchel Nambiar', 'hisrchel'),
    ('Johnny Meshramkar', 'johnny'),
    ('Rohan Thandu', 'rohan'),
    ('Eric Ming', 'eric'),
    ('John Webster', 'john'),
    ('William Beard', 'william'),
    ('Tony Capiello', 'tony'),
    ('Yash Patel', 'yash'),
    ('Vibhav Kumar', 'vibhav'),
    ('Ethan Stone', 'ethan'),
    ('Ethan Dilley', 'ethan'),
    ('Tommy Zaffiro', 'tommy'),
    ('Anwar Adous', 'anwar'),
    ('Hisrchel', 'hisrchel'),
    ('Johnny', 'johnny'),
    ('Rohan', 'rohan'),
    ('Eric', 'eric'),
    ('John', 'john'),
    ('William', 'william'),
    ('Tony', 'tony'),
    ('Yash', 'yash'),
    ('Vibhav', 'vibhav'),
    ('Ethan', 'ethan'),
    ('Tommy', 'tommy'),
    ('Anwar', 'anwar')
)
UPDATE players p
SET owner = m.new_owner
FROM owner_map m
WHERE lower(trim(p.owner)) = lower(m.old_owner)
  AND p.owner <> m.new_owner;

-- +goose Down
SELECT 1;
