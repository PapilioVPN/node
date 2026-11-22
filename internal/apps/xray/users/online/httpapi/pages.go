package httpapi

import (
	"context"
	"strconv"

	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/online/infra/jsonwire"
	"github.com/gofiber/fiber/v3"
)

func pageSelection(request fiber.Ctx, ips bool) (domain.PageRequest, error) {
	r := domain.PageRequest{Selection: domain.Selection{InstanceID: request.Params("id")}}
	if len(request.Body()) != 0 {
		return r, domain.ErrInvalidArgument
	}
	seen := map[string]bool{}
	var err error
	request.Request().URI().QueryArgs().VisitAll(func(key, value []byte) {
		name := string(key)
		if seen[name] {
			err = domain.ErrInvalidArgument
			return
		}
		seen[name] = true
		switch name {
		case "page_size":
			size, e := strconv.Atoi(string(value))
			if e != nil {
				err = domain.ErrInvalidArgument
				return
			}
			r.PageSize = size
		case "page_token":
			r.PageToken = string(value)
		case "email":
			if !ips {
				err = domain.ErrInvalidArgument
				return
			}
			r.Email = string(value)
		default:
			err = domain.ErrInvalidArgument
		}
	})
	if ips && !seen["email"] {
		err = domain.ErrInvalidArgument
	}
	return r, err
}
func (h Handler) page(request fiber.Ctx, operation func(context.Context, domain.PageRequest) (domain.Page, error), ips bool) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	r, err := pageSelection(request, ips)
	if err != nil {
		return failure(request, err)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	p, err := operation(ctx, r)
	if err != nil {
		return failure(request, err)
	}
	var data []byte
	if ips {
		data, err = jsonwire.EncodeIPPage(p)
	} else {
		data, err = jsonwire.EncodePage(p)
	}
	if err != nil {
		return failure(request, err)
	}
	request.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return request.Send(data)
}
