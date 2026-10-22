package models

import "time"

type Page struct {
	ID          int64
	PublicID    string
	EditToken   string
	Title       string
	Content     string
	Author      string
	IsPublished bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (p *Page) IsEmpty() bool {
	return p.Title == "" && p.Content == ""
}

type Image struct {
	ID       int64
	FileName string

	OrphanedAt *time.Time
}
