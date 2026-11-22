package validators

import "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"

func SourceSelections(items []domain.SourceSelection) error {
	if len(items) == 0 {
		return domain.ErrInvalidArgument
	}
	if len(items) > domain.MaxBatchItems {
		return domain.ErrCapacity
	}
	for _, item := range items {
		if err := Selector(item.InboundTag); err != nil {
			return err
		}
		if err := Email(item.Email); err != nil {
			return err
		}
	}
	return nil
}

func Additions(items []domain.Addition) error {
	if len(items) == 0 {
		return domain.ErrInvalidArgument
	}
	if len(items) > domain.MaxBatchItems {
		return domain.ErrCapacity
	}
	remaining := domain.MaxBatchBytes
	for _, item := range items {
		if err := Selector(item.InboundTag); err != nil {
			return err
		}
		if err := Configuration(item.Configuration); err != nil {
			return err
		}
		if len(item.Configuration) > remaining {
			return domain.ErrCapacity
		}
		remaining -= len(item.Configuration)
	}
	return nil
}

func Removals(items []domain.Removal) error {
	if len(items) == 0 {
		return domain.ErrInvalidArgument
	}
	if len(items) > domain.MaxBatchItems {
		return domain.ErrCapacity
	}
	for _, item := range items {
		if err := Selector(item.InboundTag); err != nil {
			return err
		}
		if err := Email(item.Email); err != nil {
			return err
		}
	}
	return nil
}
