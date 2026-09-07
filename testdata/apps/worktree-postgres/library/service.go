package library

import (
	"context"
	"database/sql"
	"errors"

	contract "example.com/library-desk/library/scenerycontract"
	"scenery.sh/datasource"
)

type Service struct{ database datasource.SQL }

func NewService(_ context.Context, input contract.LibraryConstructorInput) (*Service, error) {
	return &Service{database: input.Dependencies.Database}, nil
}

func (s *Service) Create(ctx context.Context, input contract.CreateInput) (contract.CreateOutcome, error) {
	var book contract.Book
	err := s.database.QueryRowContext(ctx, `INSERT INTO books (book_id, title, borrower) VALUES ($1, $2, '') ON CONFLICT (book_id) DO NOTHING RETURNING book_id, title, borrower`, input.BookId, input.Title).Scan(&book.BookId, &book.Title, &book.Borrower)
	if errors.Is(err, sql.ErrNoRows) {
		return contract.CreateConflict{Value: contract.Lookup{BookId: input.BookId}}, nil
	}
	if err != nil {
		return nil, err
	}
	return contract.CreateCreated{Value: book}, nil
}

func (s *Service) List(ctx context.Context, _ contract.ListInput) (contract.ListOutcome, error) {
	rows, err := s.database.QueryContext(ctx, `SELECT book_id, title, borrower FROM books ORDER BY book_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	books := make([]contract.Book, 0)
	for rows.Next() {
		var book contract.Book
		if err := rows.Scan(&book.BookId, &book.Title, &book.Borrower); err != nil {
			return nil, err
		}
		books = append(books, book)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return contract.ListListed{Value: contract.BookList{Books: books}}, nil
}

// The conditional update is the lending invariant, including concurrent requests.
func (s *Service) Borrow(ctx context.Context, input contract.BorrowInput) (contract.BorrowOutcome, error) {
	var book contract.Book
	err := s.database.QueryRowContext(ctx, `UPDATE books SET borrower = $2 WHERE book_id = $1 AND borrower = '' RETURNING book_id, title, borrower`, input.BookId, input.Loan.Borrower).Scan(&book.BookId, &book.Title, &book.Borrower)
	if errors.Is(err, sql.ErrNoRows) {
		exists, err := s.exists(ctx, input.BookId)
		if err != nil {
			return nil, err
		}
		lookup := contract.Lookup{BookId: input.BookId}
		if !exists {
			return contract.BorrowMissing{Value: lookup}, nil
		}
		return contract.BorrowConflict{Value: lookup}, nil
	}
	if err != nil {
		return nil, err
	}
	return contract.BorrowBorrowed{Value: book}, nil
}

func (s *Service) Return(ctx context.Context, input contract.ReturnInput) (contract.ReturnOutcome, error) {
	var book contract.Book
	err := s.database.QueryRowContext(ctx, `UPDATE books SET borrower = '' WHERE book_id = $1 AND borrower <> '' RETURNING book_id, title, borrower`, input.BookId).Scan(&book.BookId, &book.Title, &book.Borrower)
	if errors.Is(err, sql.ErrNoRows) {
		exists, err := s.exists(ctx, input.BookId)
		if err != nil {
			return nil, err
		}
		if !exists {
			return contract.ReturnMissing{Value: input}, nil
		}
		return contract.ReturnConflict{Value: input}, nil
	}
	if err != nil {
		return nil, err
	}
	return contract.ReturnReturned{Value: book}, nil
}

func (s *Service) exists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := s.database.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM books WHERE book_id = $1)`, id).Scan(&exists)
	return exists, err
}
